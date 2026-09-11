package cwlcore

import "slices"

// Requirement and hint scoping.
// Inner-most wins, requirements beat hints, no field-level merge.

// reqFrame is one carrier's requirements and hints, plus its process class.
type reqFrame struct {
	class string
	reqs  []ProcessRequirement
	hints []Hint
}

// RequirementOrigin indicates whether a resolved requirement came from requirements or hints.
type RequirementOrigin string

// The lists a declaration can come from.
const (
	// OriginNone is reported when no declaration of the class was found.
	OriginNone RequirementOrigin = ""

	// OriginRequirements marks a declaration from a requirements list.
	OriginRequirements RequirementOrigin = "requirements"

	// OriginHints marks a declaration from a hints list (advisory).
	OriginHints RequirementOrigin = "hints"
)

// RequirementScope is an immutable chain of requirement/hint frames, outer to inner.
// Zero value is a valid empty scope.
type RequirementScope struct {
	// frames are the raw frames, outer to inner.
	frames []reqFrame

	// view is frames with the inheritance-validity filter applied.
	view []reqFrame
}

// NewScope builds a scope from a process's requirements and hints.
func NewScope(p Process) *RequirementScope {
	if p == nil {
		return &RequirementScope{frames: nil, view: nil}
	}

	base := p.Base()

	return newScope([]reqFrame{{class: p.Class(), reqs: base.Requirements, hints: base.Hints}})
}

// Push appends a non-process frame (e.g. a workflow step).
func (s *RequirementScope) Push(reqs []ProcessRequirement, hints []Hint) *RequirementScope {
	return s.push(reqFrame{class: "", reqs: reqs, hints: hints})
}

// PushProcess appends a process frame, recording its class for the inheritance filter.
func (s *RequirementScope) PushProcess(p Process) *RequirementScope {
	if p == nil {
		return s
	}

	base := p.Base()

	return s.push(reqFrame{class: p.Class(), reqs: base.Requirements, hints: base.Hints})
}

// GetRequirement returns the in-effect declaration of class.
// Requirements beat hints; inner-most wins.
func (s *RequirementScope) GetRequirement(class string) (ProcessRequirement, bool, RequirementOrigin) {
	for _, f := range slices.Backward(s.view) {
		if r, ok := lastOfClass(f.reqs, class); ok {
			return r, true, OriginRequirements
		}
	}

	for _, f := range slices.Backward(s.view) {
		if h, ok := lastOfClass(f.hints, class); ok {
			return hintAsRequirement(h), true, OriginHints
		}
	}

	return nil, false, OriginNone
}

// EffectiveRequirements returns one entry per class, deduplicated (last wins).
func (s *RequirementScope) EffectiveRequirements() []ProcessRequirement {
	all := make([]ProcessRequirement, 0, len(s.view))
	for _, f := range s.view {
		all = append(all, f.reqs...)
	}

	return dedupKeepLast(all)
}

// EffectiveHints returns all hints in scope, outer to inner, without deduplication.
func (s *RequirementScope) EffectiveHints() []Hint {
	all := make([]Hint, 0, len(s.view))
	for _, f := range s.view {
		all = append(all, f.hints...)
	}

	return all
}

// push appends one frame, leaving the receiver untouched.
func (s *RequirementScope) push(f reqFrame) *RequirementScope {
	frames := make([]reqFrame, 0, len(s.frames)+1)
	frames = append(frames, s.frames...)

	return newScope(append(frames, f))
}

// newScope takes ownership of frames and computes the filtered view once.
func newScope(frames []reqFrame) *RequirementScope {
	return &RequirementScope{frames: frames, view: filterFrames(frames)}
}

// lastOfClass returns the last entry declaring class within a frame.
func lastOfClass[T Hint](entries []T, class string) (T, bool) {
	for _, e := range slices.Backward(entries) {
		if e.Class() == class {
			return e, true
		}
	}

	var zero T

	return zero, false
}

// dedupKeepLast keeps the last entry per class.
func dedupKeepLast[T Hint](entries []T) []T {
	out := make([]T, 0, len(entries))
	seen := make(map[string]bool, len(entries))

	for _, e := range slices.Backward(entries) {
		class := e.Class()
		if seen[class] {
			continue
		}

		seen[class] = true

		out = append(out, e)
	}

	slices.Reverse(out)

	return out
}

// hintAsRequirement converts a Hint to ProcessRequirement.
func hintAsRequirement(h Hint) ProcessRequirement {
	if r, ok := h.(ProcessRequirement); ok {
		return r
	}

	if raw, ok := h.(*RawHint); ok {
		return &RawRequirement{requirementBase: requirementBase{}, Node: raw.Node, ClassIRI: raw.ClassIRI}
	}

	return &RawRequirement{requirementBase: requirementBase{}, Node: nil, ClassIRI: h.Class()}
}
