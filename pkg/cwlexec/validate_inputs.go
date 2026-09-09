package cwlexec

import (
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Input validation errors.
var (
	// ErrInputRequired reports a required input that is missing from the input object and declares
	// no default.
	ErrInputRequired = errors.New("required input is missing and has no default")

	// ErrInputUnknown reports an input key that names no declared parameter of the process.
	ErrInputUnknown = errors.New("input key names no declared parameter")
)

// ValidateOption configures the behavior of [ValidateInputs].
type ValidateOption func(*validateSettings)

type validateSettings struct {
	rejectUnknown bool
}

// WithRejectUnknown makes [ValidateInputs] return an error for input keys that name no declared
// parameter. The default is to silently drop them, matching the CWL specification's guidance that
// undeclared inputs are not an error.
func WithRejectUnknown() ValidateOption {
	return func(s *validateSettings) { s.rejectUnknown = true }
}

// ValidateInputs checks that inputs satisfies the declared input parameters of process: every
// required input is present, every supplied value inhabits its declared type, and — when
// [WithRejectUnknown] is set — no undeclared inputs appear.
//
// On success it returns a merged map containing every supplied value that passed type-checking
// plus default values for any declared input the caller omitted. The caller may pass the merged
// map directly to [Runner.Run]; the scheduler's own default-fill pass is idempotent on keys that
// are already present.
//
// On failure it returns nil and an error that wraps every violation found, joined with
// [errors.Join]. Individual violations wrap [ErrInputRequired], [ErrOutputType], or
// [ErrInputUnknown] and can be tested with [errors.Is].
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
		unknown := make([]string, 0)

		for key := range inputs {
			if !declared[key] {
				unknown = append(unknown, key)
			}
		}

		slices.Sort(unknown)

		for _, key := range unknown {
			problems = append(problems, fmt.Errorf("%w: %q", ErrInputUnknown, key))
		}
	}

	merged := make(map[string]any, len(decls))

	for i := range decls {
		decl := &decls[i]
		value, supplied := inputs[decl.Name]

		if supplied && value != nil {
			err := checkValueType(value, decl.Type)
			if err != nil {
				problems = append(problems, fmt.Errorf("input %q: %w", decl.Name, err))

				continue
			}

			merged[decl.Name] = value

			continue
		}

		if decl.Default != nil {
			merged[decl.Name] = decl.Default

			continue
		}

		if decl.Type.IsOptional() || decl.Type.IsNull() {
			merged[decl.Name] = nil

			continue
		}

		problems = append(problems, fmt.Errorf("input %q: %w: type is %s", decl.Name, ErrInputRequired, decl.Type))
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}

	return merged, nil
}
