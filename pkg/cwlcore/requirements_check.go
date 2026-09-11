package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Fail-closed gate on unrecognized requirements.
// Hints are never reported. [WithLenient] downgrades to warnings.

// unknownRequirementMsg is the error format for an unrecognized requirement class.
const unknownRequirementMsg = "unrecognized process requirement %q: " +
	"this implementation cannot satisfy it, so the process must not run"

// checkConfig holds the options CheckKnown accepts.
type checkConfig struct {
	lenient bool
	warn    func(*salad.Error)
}

// CheckOption configures [RequirementScope.CheckKnown].
type CheckOption func(*checkConfig)

// WithLenient downgrades unrecognized-requirement errors to warnings.
func WithLenient() CheckOption {
	return func(c *checkConfig) {
		c.lenient = true
	}
}

// WithWarnFunc supplies a sink for findings downgraded by [WithLenient].
func WithWarnFunc(fn func(*salad.Error)) CheckOption {
	return func(c *checkConfig) {
		c.warn = fn
	}
}

// CheckKnown returns an error if any requirement in scope is unrecognized.
// Core CWL v1.2 classes and allowExtensions entries pass; everything else fails.
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

// unknownRequirements collects one error per unrecognized requirement class.
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

// requirementLoc returns the source location of a requirement, if available.
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
