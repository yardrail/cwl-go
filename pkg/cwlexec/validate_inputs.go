package cwlexec

import (
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Input validation errors.
var (
	// ErrInputRequired reports a required input with no value and no default.
	ErrInputRequired = errors.New("required input is missing and has no default")
	// ErrInputUnknown reports an input key not matching any declared parameter.
	ErrInputUnknown = errors.New("input key names no declared parameter")
)

// ValidateOption configures the behavior of [ValidateInputs].
type ValidateOption func(*validateSettings)

type validateSettings struct {
	rejectUnknown bool
}

// WithRejectUnknown makes [ValidateInputs] error on undeclared input keys.
func WithRejectUnknown() ValidateOption {
	return func(s *validateSettings) { s.rejectUnknown = true }
}

// ValidateInputs type-checks inputs against the process's declared parameters.
// Returns a merged map with defaults filled in, or a joined error of all violations.
func ValidateInputs(
	process cwlcore.Process,
	inputs map[string]any,
	opts ...ValidateOption,
) (map[string]any, error) {
	var settings validateSettings
	for _, opt := range opts {
		opt(&settings)
	}

	decls := inputDecls(process)

	declared := make(map[string]bool, len(decls))
	for i := range decls {
		declared[decls[i].Name] = true
	}

	var problems []error

	if settings.rejectUnknown {
		problems = append(problems, rejectUnknownInputs(inputs, declared)...)
	}

	merged := make(map[string]any, len(decls))

	for i := range decls {
		decl := &decls[i]

		err := resolveInputValue(decl, inputs, merged)
		if err != nil {
			problems = append(problems, err)
		}
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}

	return merged, nil
}

// rejectUnknownInputs returns an error for each key in inputs that does not appear in declared.
func rejectUnknownInputs(inputs map[string]any, declared map[string]bool) []error {
	unknown := make([]string, 0)

	for key := range inputs {
		if !declared[key] {
			unknown = append(unknown, key)
		}
	}

	slices.Sort(unknown)

	errs := make([]error, 0, len(unknown))
	for _, key := range unknown {
		errs = append(errs, fmt.Errorf("%w: %q", ErrInputUnknown, key))
	}

	return errs
}

// resolveInputValue resolves one declared input: type-check, default, optional nil, or error.
func resolveInputValue(decl *portDecl, inputs, merged map[string]any) error {
	value, supplied := inputs[decl.Name]

	if supplied && value != nil {
		err := checkValueType(value, decl.Type)
		if err != nil {
			return fmt.Errorf("input %q: %w", decl.Name, err)
		}

		merged[decl.Name] = value

		return nil
	}

	if decl.Default != nil {
		merged[decl.Name] = decl.Default

		return nil
	}

	if decl.Type.IsOptional() || decl.Type.IsNull() {
		merged[decl.Name] = nil

		return nil
	}

	return fmt.Errorf("input %q: %w: type is %s", decl.Name, ErrInputRequired, decl.Type)
}
