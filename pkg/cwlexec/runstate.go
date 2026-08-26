package cwlexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// RunStateVersion is the schema version [RunState] serializes itself as.
//
// It is written from the first release rather than added when the schema first changes, because a
// state persisted without one cannot be told apart from a state persisted with a different shape.
// [Runner.Resume] rejects any other version outright: rehydrating a snapshot whose meaning has
// moved would resume a run that never existed. Migrating a persisted snapshot across versions
// belongs to whoever persisted it.
const RunStateVersion = 1

// ErrStateVersion reports a [RunState] whose version is not [RunStateVersion].
var ErrStateVersion = errors.New("run state version mismatch")

// suspensionJSON is the wire shape of a [Suspension].
//
// It exists because a snapshot is persisted as JSON and every field of it must therefore carry an
// explicit name: the shape a caller stores is part of this package's contract, and letting it be
// derived from Go field names would make renaming one a silent format change. Token and Payload are
// copied through untouched — nothing here reads them.
type suspensionJSON struct {
	StepID       string `json:"stepId"`
	Token        string `json:"token,omitempty"`
	Payload      []byte `json:"payload,omitempty"`
	ScatterIndex []int  `json:"scatterIndex,omitempty"`
}

// asSuspension renders the wire shape back as the value a caller is handed.
func (s *suspensionJSON) asSuspension() Suspension {
	return Suspension{StepID: s.StepID, ScatterIndex: s.ScatterIndex, Token: s.Token, Payload: s.Payload}
}

// wireSuspension renders a suspension in the wire shape a snapshot records it as.
func wireSuspension(suspension *Suspension) *suspensionJSON {
	if suspension == nil {
		return nil
	}

	return &suspensionJSON{
		StepID:       suspension.StepID,
		Token:        suspension.Token,
		Payload:      suspension.Payload,
		ScatterIndex: suspension.ScatterIndex,
	}
}

// jobState is the recorded outcome of one invocation: one sub-job of a scattered step, or the whole
// of an unscattered one.
type jobState struct {
	// Outputs is the invocation's output object, projected onto the step's declared out ports.
	Outputs map[string]any `json:"outputs,omitempty"`

	// Suspension is the handle a suspended invocation produced, and nil for every other status.
	Suspension *suspensionJSON `json:"suspension,omitempty"`

	// Status is the invocation's outcome, empty while it has none yet.
	Status Status `json:"status,omitempty"`

	// Error is the rendered failure, kept as text because an error does not survive a JSON round
	// trip and a resumed run still has to be able to say why a branch failed.
	Error string `json:"error,omitempty"`

	// Index is the invocation's scatter coordinates, empty for an unscattered step. Together with
	// the step id it addresses the invocation across the whole run.
	Index []int `json:"index,omitempty"`
}

// terminal reports whether the invocation has an outcome that will not change. A suspended
// invocation is deliberately not terminal: it is waiting, and only a resume moves it on.
func (j *jobState) terminal() bool {
	return j.Status != "" && j.Status != StatusSuspended
}

// stepState is the recorded progress of one step.
type stepState struct {
	// Outputs is the step's output object once it has finished, keyed by its declared out ports.
	Outputs map[string]any `json:"outputs,omitempty"`

	// Status is the step's outcome, empty until every one of its invocations is terminal.
	Status Status `json:"status,omitempty"`

	// Error is the rendered failure that gave the step its status.
	Error string `json:"error,omitempty"`

	// Jobs are the step's invocations, in the order the scatter expansion produced them.
	Jobs []jobState `json:"jobs,omitempty"`

	// Shape is the nesting shape a scattered step's outputs gather into, empty when the step is
	// not scattered. It is recorded rather than re-derived so that a resumed run can gather a
	// step whose remaining work was only ever a suspended slot.
	Shape []int `json:"shape,omitempty"`

	// Started records that the step's inputs have been resolved and its invocations enumerated.
	Started bool `json:"started,omitempty"`
}

// runStateJSON is the wire shape of a [RunState]. It exists so that the state's fields can stay
// unexported — a caller persists the snapshot, it does not reach into it.
type runStateJSON struct {
	Inputs  map[string]any        `json:"inputs,omitempty"`
	Steps   map[string]*stepState `json:"steps,omitempty"`
	Version int                   `json:"version"`
}

// RunState is a serializable snapshot of a run: the input object it was started with, and for every
// step its progress, its accumulated outputs, its per-index scatter progress and any suspension
// outstanding against it.
//
// It is the whole of what [Runner.Resume] needs. Nothing else is carried between a suspended run
// and its resumption — no goroutine, no timer, no open file — which is what lets a suspended run
// survive a process restart, and what makes a pause cost nothing to hold.
//
// The value is opaque: it is marshaled to JSON, persisted, and handed back. The embedded version
// field is checked on resume; see [RunStateVersion].
//
// A RunState shares its maps with the run that produced it and with any copy of itself, so it must
// not be mutated. Marshal it, or hand it straight back to Resume.
type RunState struct {
	steps   map[string]*stepState
	inputs  map[string]any
	version int
}

// Compile-time proof that a RunState is exactly as persistable as it claims to be.
var (
	_ json.Marshaler   = RunState{}
	_ json.Unmarshaler = (*RunState)(nil)
)

// newRunState returns an empty snapshot stamped with the current version, for a run that is about
// to start.
func newRunState(inputs map[string]any) *RunState {
	return &RunState{
		steps:   make(map[string]*stepState),
		inputs:  maps.Clone(inputs),
		version: RunStateVersion,
	}
}

// MarshalJSON renders the snapshot, version field included, as the JSON a caller persists.
func (s RunState) MarshalJSON() ([]byte, error) {
	return json.Marshal(runStateJSON{Inputs: s.inputs, Steps: s.steps, Version: s.version})
}

// UnmarshalJSON restores a snapshot from persisted JSON.
//
// It deliberately accepts a version it does not understand rather than failing here, so that a
// caller can read an old snapshot back and inspect it. The version is enforced where it matters, at
// [Runner.Resume].
func (s *RunState) UnmarshalJSON(data []byte) error {
	var wire runStateJSON

	err := json.Unmarshal(data, &wire)
	if err != nil {
		return fmt.Errorf("cwlexec: decoding a RunState: %w", err)
	}

	s.inputs = wire.Inputs
	s.steps = wire.Steps
	s.version = wire.Version

	if s.steps == nil {
		s.steps = make(map[string]*stepState)
	}

	return nil
}

// step returns the recorded progress of a step, creating an empty record the first time it is
// asked for.
func (s *RunState) step(id string) *stepState {
	recorded, found := s.steps[id]
	if !found {
		recorded = &stepState{}
		s.steps[id] = recorded
	}

	return recorded
}

// clone returns a snapshot that shares no mutable structure with the receiver's step records, so
// that a result handed to a caller cannot be changed by a run that is still going.
//
// Values inside the output objects are shared, not copied. They are the run's data, they are
// treated as immutable everywhere in this package, and deep-copying every File object of a large
// scatter to hand back a snapshot would be a real cost for no gain.
func (s *RunState) clone() RunState {
	steps := make(map[string]*stepState, len(s.steps))

	for id, recorded := range s.steps {
		copied := *recorded
		copied.Outputs = maps.Clone(recorded.Outputs)
		copied.Jobs = slices.Clone(recorded.Jobs)
		copied.Shape = slices.Clone(recorded.Shape)
		steps[id] = &copied
	}

	return RunState{steps: steps, inputs: maps.Clone(s.inputs), version: s.version}
}

// rehydrate returns a mutable copy of a caller-supplied snapshot, after checking that its version
// is one this engine understands.
func (s *RunState) rehydrate() (*RunState, error) {
	if s.version != RunStateVersion {
		return nil, fmt.Errorf("%w: state is version %d, this engine writes and reads version %d",
			ErrStateVersion, s.version, RunStateVersion)
	}

	restored := s.clone()

	return &restored, nil
}
