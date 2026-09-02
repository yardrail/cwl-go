package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The fail-closed gate on requirements this implementation does not recognize.
//
// The specification is unambiguous about what must happen, and it is the
// opposite of the permissive default most document formats take:
//
//	If an implementation cannot satisfy all requirements, or a requirement is
//	listed which is not recognized by the implementation, it is a fatal error
//	and the implementation must not attempt to run the process, unless
//	overridden at user option.
//
// Hints get the opposite treatment in the same passage — "it is not an error if
// an implementation cannot satisfy all hints" — so CheckKnown never reports
// one, no matter how strange its class.
//
// "Unless overridden at user option" is what WithLenient is. It exists because
// the specification provides for it, not as a convenience, and it is not the
// default. When it is in effect CheckKnown returns nil and the findings go to
// the WithWarnFunc sink, if one was given: an error value means "do not run",
// always, and an override that still returns one would be no override at all.

// unknownRequirementMsg is what CheckKnown reports for one offending class. It
// names the class, because "unrecognized requirement" on its own tells a user
// nothing about which extension their engine is missing.
const unknownRequirementMsg = "unrecognized process requirement %q: " +
	"this implementation cannot satisfy it, so the process must not run"

// checkConfig holds the options CheckKnown accepts.
type checkConfig struct {
	lenient bool
	warn    func(*salad.Error)
}

// CheckOption configures [RequirementScope.CheckKnown].
type CheckOption func(*checkConfig)

// WithLenient downgrades [RequirementScope.CheckKnown]'s failures to warnings,
// so that an unrecognized requirement no longer stops the run. It is the
// specification's "unless overridden at user option", and belongs behind a
// deliberate user choice such as a command-line flag.
//
// A downgraded finding is not returned. CheckKnown returns nil, because a
// non-nil error means failure to every caller and to every linter, and a
// downgrade that still fails the run is not a downgrade:
//
//	if err := scope.CheckKnown(allowed, opts...); err != nil {
//		return Result{Status: StatusPermanentFail}, err
//	}
//
// That call site is the one that will be written, and with WithLenient in play
// it must not fire. Pass [WithWarnFunc] alongside this option to see what was
// downgraded; with no sink the finding is dropped, which is what a caller who
// asked to proceed anyway has asked for.
func WithLenient() CheckOption {
	return func(c *checkConfig) {
		c.lenient = true
	}
}

// WithWarnFunc supplies a sink for the findings [WithLenient] downgrades.
// Each is a [salad.Error] flagged as a warning, naming the offending class and
// carrying its source line, so a caller can log exactly what it agreed to
// ignore.
//
// The sink observes downgraded findings and nothing else. Without WithLenient
// nothing is downgraded, so it is never called and the option is a harmless
// no-op: a fatal check reports through its returned error, as it must. The rule
// is therefore the whole contract — fn sees precisely those findings that were
// suppressed.
//
// fn is called once per offending requirement, in scope order — outer frame
// first, and within a frame in document order — so a lenient check surfaces
// every offender, where a fatal one names only the first.
func WithWarnFunc(fn func(*salad.Error)) CheckOption {
	return func(c *checkConfig) {
		c.warn = fn
	}
}

// CheckKnown reports whether every requirement declared anywhere in the scope
// is one this implementation can be expected to honour: a core CWL v1.2
// requirement, or an extension class the caller vouches for by mapping it to
// true in allowExtensions. It returns an error naming the first class that is
// neither, and nil when every requirement is accounted for.
//
// This is the gate that makes running an untrusted document safe. A requirement
// is a demand about how the process must be run, so an engine that does not
// understand one cannot know whether it is honouring it; the specification
// therefore makes an unrecognized requirement fatal, and so does this.
//
// [WithLenient] downgrades that: the check then returns nil however many
// offenders it found, handing each to the sink [WithWarnFunc] supplies if there
// is one. A returned error therefore always means the process must not run.
//
// Hints are never reported. A hint is advisory by definition, and an
// implementation is required to ignore one it does not understand rather than
// refuse the document.
//
// Every frame is checked, not only those a lookup would see: a requirement the
// inheritance-validity filter keeps from reaching a tool was still declared,
// and if this implementation cannot honour it the workflow it was declared on
// must not run either. The filter never removes an extension class in any case,
// so it could not hide an offender.
//
// allowExtensions gates class names only. Whether the contents of an extension
// requirement are well-formed is a question for the package that claims the
// class, which reads them from [RawRequirement.Node]. A nil map vouches for
// nothing, which is the correct default.
func (s *RequirementScope) CheckKnown(allowExtensions map[string]bool, opts ...CheckOption) error {
	var cfg checkConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	offenders := s.unknownRequirements(allowExtensions)
	if len(offenders) == 0 {
		return nil
	}

	if !cfg.lenient {
		return offenders[0]
	}

	if cfg.warn == nil {
		return nil
	}

	for _, e := range offenders {
		e.Warning = true
		cfg.warn(e)
	}

	return nil
}

// unknownRequirements collects one error per requirement whose class is neither
// core nor vouched for, in scope order: outer frame first, and within a frame
// in document order. The first is therefore the outer-most, earliest offender.
func (s *RequirementScope) unknownRequirements(allowExtensions map[string]bool) []*salad.Error {
	offenders := make([]*salad.Error, 0, len(s.frames))

	for _, f := range s.frames {
		for _, r := range f.reqs {
			class := r.Class()
			if IsCoreRequirement(class) || allowExtensions[class] {
				continue
			}

			offenders = append(offenders, salad.Errorf(requirementLoc(r), unknownRequirementMsg, class))
		}
	}

	return offenders
}

// requirementLoc reports where a requirement was written, when that is
// recoverable. Only a [RawRequirement] keeps its source node, which is enough:
// it is the only kind CheckKnown ever rejects.
func requirementLoc(r ProcessRequirement) salad.SourceLine {
	raw, ok := r.(*RawRequirement)
	if !ok || raw.Node == nil {
		return salad.SourceLine{
			File:  "",
			Start: salad.Position{Line: 0, Column: 0, Offset: 0},
			End:   salad.Position{Line: 0, Column: 0, Offset: 0},
		}
	}

	return raw.Node.Loc()
}
