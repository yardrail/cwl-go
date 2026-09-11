package cwlexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// RunStateVersion is the schema version [RunState] serializes as.
// [Runner.Resume] rejects mismatches.
const RunStateVersion = 1

// ErrStateVersion reports a [RunState] whose version is not [RunStateVersion].
var ErrStateVersion = errors.New("run state version mismatch")

// suspensionJSON is the JSON wire shape of a [Suspension].
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

// jobState records the outcome of one invocation (scatter sub-job or whole step).
type jobState struct {
	Outputs    map[string]any  `json:"outputs,omitempty"`
	Suspension *suspensionJSON `json:"suspension,omitempty"`
	Status     Status          `json:"status,omitempty"`
	Error      string          `json:"error,omitempty"` // Text since errors don't survive JSON.
	Index      []int           `json:"index,omitempty"` // Scatter coordinates; empty if unscattered.
}

// terminal reports whether this invocation has a final outcome (not suspended).
func (j *jobState) terminal() bool {
	return j.Status != "" && j.Status != StatusSuspended
}

// stepState is the recorded progress of one step.
type stepState struct {
	Outputs map[string]any `json:"outputs,omitempty"`
	Status  Status         `json:"status,omitempty"`
	Error   string         `json:"error,omitempty"`
	Jobs    []jobState     `json:"jobs,omitempty"`
	Shape   []int          `json:"shape,omitempty"`   // Scatter output nesting shape.
	Started bool           `json:"started,omitempty"` // True once inputs are resolved.
}

// runStateJSON is the JSON wire shape of [RunState].
type runStateJSON struct {
	Inputs  map[string]any        `json:"inputs,omitempty"`
	Steps   map[string]*stepState `json:"steps,omitempty"`
	Version int                   `json:"version"`
}

// RunState is a serializable snapshot of a run's progress.
// Opaque: marshal to JSON, persist, hand back to [Runner.Resume]. Do not mutate.
type RunState struct {
	steps   map[string]*stepState
	inputs  map[string]any
	version int
}

// newRunState returns a version-stamped empty snapshot for a new run.
func newRunState(inputs map[string]any) *RunState {
	return &RunState{
		steps:   make(map[string]*stepState),
		inputs:  maps.Clone(inputs),
		version: RunStateVersion,
	}
}

// MarshalJSON renders the snapshot, version field included, as the JSON a caller persists.
func (s *RunState) MarshalJSON() ([]byte, error) {
	return json.Marshal(runStateJSON{Inputs: s.inputs, Steps: s.steps, Version: s.version})
}

// UnmarshalJSON restores a snapshot from JSON. Version is checked at [Runner.Resume], not here.
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

// step returns a step's progress record, creating one on first access.
func (s *RunState) step(id string) *stepState {
	recorded, found := s.steps[id]
	if !found {
		recorded = &stepState{Outputs: nil, Status: "", Error: "", Jobs: nil, Shape: nil, Started: false}
		s.steps[id] = recorded
	}

	return recorded
}

// clone returns a shallow copy that shares output values but not mutable step records.
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

// rehydrate returns a mutable copy after version validation.
func (s *RunState) rehydrate() (*RunState, error) {
	if s.version != RunStateVersion {
		return nil, fmt.Errorf("%w: state is version %d, this engine writes and reads version %d",
			ErrStateVersion, s.version, RunStateVersion)
	}

	restored := s.clone()

	return &restored, nil
}
