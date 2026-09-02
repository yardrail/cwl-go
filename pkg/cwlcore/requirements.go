package cwlcore

import "slices"

// Requirement and hint scoping: which declaration of a requirement class is in
// effect for a given process, and how declarations made at different levels of
// a workflow combine.
//
// The model is a chain of frames ordered outer to inner — a workflow, then one
// of its steps, then the process embedded under that step's run — each frame
// holding one carrier's requirements and hints exactly as they were written.
// Nothing is merged when a frame is pushed; precedence is resolved when the
// scope is queried. Three rules do all the work, and they are the rules the
// specification states:
//
//  1. Requirements beat hints. A hint is consulted only when no frame declares
//     the class in requirements. "Requirements override hints."
//
//  2. Inner-most wins, and within one frame the last declaration wins. "The
//     most specific instance of the requirement is used."
//
//  3. A winning declaration replaces the one it beats outright. There is no
//     field-level merge: an inner ResourceRequirement setting only coresMin
//     does not inherit an outer one's ramMin. The outer declaration is gone,
//     not extended.
//
// One filter runs ahead of all three; see filterFrames.

// reqFrame is one carrier's contribution to a scope: the requirements and hints
// it declared, in document order, plus the class of the process that declared
// them.
//
// class is empty for a frame pushed by [RequirementScope.Push], which carries a
// workflow step's own requirements and hints rather than a process's. Only a
// frame with a known class can be the target of the inheritance-validity
// filter.
type reqFrame struct {
	class string
	reqs  []ProcessRequirement
	hints []Hint
}

// RequirementOrigin says which of a process's two declaration lists a resolved
// requirement was written in. It is the third result of
// [RequirementScope.GetRequirement], and the difference matters: a requirement
// must be honoured or the process must not run, whereas a hint may be ignored.
type RequirementOrigin string

// The lists a declaration can come from.
const (
	// OriginNone is reported when no declaration of the class was found.
	OriginNone RequirementOrigin = ""

	// OriginRequirements marks a declaration written in a requirements list,
	// which the implementation is obliged to honour.
	OriginRequirements RequirementOrigin = "requirements"

	// OriginHints marks a declaration written in a hints list, which is
	// advisory and may be ignored.
	OriginHints RequirementOrigin = "hints"
)

// RequirementScope is an ordered chain of requirement and hint frames, from the
// outer-most carrier to the inner-most, resolving which declaration of a class
// is in effect for the inner-most process.
//
// A scope is immutable: [RequirementScope.Push] and
// [RequirementScope.PushProcess] return a new scope rather than extending the
// receiver, so one workflow scope can safely be the parent of every step's
// scope. The zero value is a valid empty scope in which nothing is found.
//
// The specification's optional input-object merge — requirements supplied under
// a cwl:requirements field of the input object, "combined with any requirements
// present in the corresponding Process as if they were specified there" — is
// not implemented here. A caller that wants it appends those requirements to
// the process's own before building the scope.
type RequirementScope struct {
	// frames are the raw frames, outer to inner, exactly as pushed.
	// CheckKnown reads these, because an unrecognized requirement is fatal
	// wherever it was declared, whether or not it is inherited anywhere.
	frames []reqFrame

	// view is frames with the inheritance-validity filter already applied.
	// Every lookup and every Effective* result reads this. It is computed
	// once per push rather than per query, which is possible because a scope
	// never changes after it is built.
	view []reqFrame
}

// NewScope builds a scope whose sole, outer-most frame is p's own requirements
// and hints. Descend into a step or an embedded process with
// [RequirementScope.Push] or [RequirementScope.PushProcess].
//
// A nil process yields an empty scope.
func NewScope(p Process) *RequirementScope {
	if p == nil {
		return &RequirementScope{frames: nil, view: nil}
	}

	base := p.Base()

	return newScope([]reqFrame{{class: p.Class(), reqs: base.Requirements, hints: base.Hints}})
}

// Push returns a child scope with reqs and hints appended as a new inner-most
// frame. Use it for a carrier that is not itself a process — a workflow step,
// whose requirements sit between the workflow's and those of the process under
// its run.
//
// The frame records no process class, so it never becomes the target of the
// inheritance-validity filter. Push the process itself with
// [RequirementScope.PushProcess] when the filter should apply.
func (s *RequirementScope) Push(reqs []ProcessRequirement, hints []Hint) *RequirementScope {
	return s.push(reqFrame{class: "", reqs: reqs, hints: hints})
}

// PushProcess returns a child scope with p's own requirements and hints
// appended as a new inner-most frame, recording p's class.
//
// Recording the class is what makes the inheritance-validity filter possible:
// the specification restricts which requirements a parent Workflow passes down
// to a CommandLineTool, and a scope cannot apply that restriction without
// knowing what it is resolving for. Building the usual workflow chain therefore
// reads:
//
//	scope := cwlcore.NewScope(workflow).
//		Push(step.Requirements, step.Hints).
//		PushProcess(embeddedRun)
//
// A nil process leaves the scope unchanged.
func (s *RequirementScope) PushProcess(p Process) *RequirementScope {
	if p == nil {
		return s
	}

	base := p.Base()

	return s.push(reqFrame{class: p.Class(), reqs: base.Requirements, hints: base.Hints})
}

// GetRequirement returns the declaration of class that is in effect, whether
// one was found at all, and which list it was written in.
//
// It scans every frame's requirements inner-most first, then — only if that
// found nothing — every frame's hints the same way, so a requirement declared
// on the outer-most workflow still beats a hint declared on the inner-most
// tool. Within a single frame the last declaration wins.
//
// A hint whose class this package models is returned as its concrete
// requirement type; a hint whose class it does not model is returned as a
// [RawRequirement] carrying the same node, so that a caller handling the
// [OriginHints] case never has to switch on hint types of its own.
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

// EffectiveRequirements returns the requirements in effect for the scope's
// inner-most process: one entry per class, each the declaration that
// [RequirementScope.GetRequirement] would return for it.
//
// Order is stable — outer to inner, and within a frame in document order — with
// each class appearing at the position of its winning declaration, so scanning
// the result backwards resolves precedence exactly as GetRequirement does.
//
// The one-entry-per-class collapse is a deliberate, contained deviation from
// cwltool, which accumulates same-class duplicates in its list and only picks a
// winner when the list is scanned. The two are indistinguishable through
// GetRequirement, which is the surface the specification describes; collapsing
// eagerly just means this list never contains a declaration that has already
// lost. Hints do the opposite — see [RequirementScope.EffectiveHints].
func (s *RequirementScope) EffectiveRequirements() []ProcessRequirement {
	all := make([]ProcessRequirement, 0, len(s.view))
	for _, f := range s.view {
		all = append(all, f.reqs...)
	}

	return dedupKeepLast(all)
}

// EffectiveHints returns every hint in scope, outer to inner and in document
// order, with no deduplication: two frames declaring the same class both
// appear, and so do two declarations within one frame.
//
// The asymmetry with [RequirementScope.EffectiveRequirements] is intentional
// rather than an oversight. A hint is advisory, and a consumer may legitimately
// want to see every advisory declaration that was made — which one wins is
// GetRequirement's answer, not this one's.
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

// lastOfClass returns the last entry declaring class, which is the one that
// wins within a single frame.
//
// [Hint] is the constraint because it is this package's spelling of "has a
// Class" — every ProcessRequirement satisfies it too, which is precisely why
// Hint is the unsealed one of the two interfaces.
func lastOfClass[T Hint](entries []T, class string) (T, bool) {
	for _, e := range slices.Backward(entries) {
		if e.Class() == class {
			return e, true
		}
	}

	var zero T

	return zero, false
}

// dedupKeepLast keeps, for each class, only the last entry declaring it, at
// that entry's own position. Preserving the winner's position rather than the
// first declaration's is what lets a backwards scan of the result agree with
// GetRequirement.
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

// hintAsRequirement re-types a hints entry as a [ProcessRequirement] so that
// GetRequirement has one result type regardless of which list the winner came
// from. Every class this package models is already a ProcessRequirement; a
// [RawHint], and any Hint a downstream package supplies, becomes an equivalent
// [RawRequirement].
func hintAsRequirement(h Hint) ProcessRequirement {
	if r, ok := h.(ProcessRequirement); ok {
		return r
	}

	if raw, ok := h.(*RawHint); ok {
		return &RawRequirement{requirementBase: requirementBase{}, Node: raw.Node, ClassIRI: raw.ClassIRI}
	}

	return &RawRequirement{requirementBase: requirementBase{}, Node: nil, ClassIRI: h.Class()}
}
