package cwlexec

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// ErrInconsistentPaths reports a File/Directory where Path and Location
// use different URI schemes, indicating a stale or partial rewrite.
var ErrInconsistentPaths = errors.New("inconsistent Path and Location")

// ValidateOutputPaths checks that every File/Directory in outputs has
// consistent Path and Location fields. Call after [OutputRewriter] to
// catch incomplete rewrites (e.g. Path rewritten to s3:// but Location
// left as a stale file:// URI).
func ValidateOutputPaths(outputs map[string]any) error {
	for name, value := range outputs {
		err := validateOutputValue(value)
		if err != nil {
			return fmt.Errorf("output %q: %w", name, err)
		}
	}

	return nil
}

func validateOutputValue(v any) error {
	switch typed := v.(type) {
	case *cwlcore.File:
		return validateOutputFile(typed)
	case *cwlcore.Directory:
		return validateOutputDir(typed)
	case []any:
		return validateOutputSlice(typed)
	case map[string]any:
		return validateOutputMap(typed)
	default:
		return nil
	}
}

func validateOutputSlice(items []any) error {
	for _, item := range items {
		err := validateOutputValue(item)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateOutputMap(items map[string]any) error {
	for _, item := range items {
		err := validateOutputValue(item)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateOutputFile(f *cwlcore.File) error {
	if f == nil {
		return nil
	}

	err := validateSchemeConsistency(f.Path(), f.Location(), f.Basename)
	if err != nil {
		return err
	}

	for _, sec := range f.SecondaryFiles {
		err = validateOutputValue(sec)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateOutputDir(d *cwlcore.Directory) error {
	if d == nil {
		return nil
	}

	err := validateSchemeConsistency(d.Path(), d.Location(), d.Basename)
	if err != nil {
		return err
	}

	for _, entry := range d.Listing {
		err := validateOutputValue(entry)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateSchemeConsistency(p, loc, basename string) error {
	if p == "" || loc == "" {
		return nil
	}

	pScheme := uriScheme(p)
	lScheme := uriScheme(loc)

	if pScheme != lScheme {
		return fmt.Errorf(
			"%w: %q has Path scheme %q but Location scheme %q",
			ErrInconsistentPaths, basename, pScheme, lScheme,
		)
	}

	return nil
}

func uriScheme(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return ""
	}

	if parsed.Scheme == "" {
		return "file"
	}

	return parsed.Scheme
}
