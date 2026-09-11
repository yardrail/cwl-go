package cwlexec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Secondary files and format resolution for output bindings.

var (
	// ErrSecondaryMissing reports a required secondary file that is not on disk.
	ErrSecondaryMissing = errors.New("required secondary file does not exist")
	// ErrSecondaryValue reports an invalid secondaryFiles expression result.
	ErrSecondaryValue = errors.New("secondaryFiles expression did not produce a name or a File")
)

// outSecondaryPolicy governs behavior when a secondary file is missing from disk.
type outSecondaryPolicy uint8

const (
	// outSecondaryOptional silently skips a missing secondary file.
	outSecondaryOptional outSecondaryPolicy = iota

	// outSecondaryRequired errors on a missing secondary file.
	outSecondaryRequired
)

// attachSecondaryFiles resolves secondaryFiles patterns against every File in value.
// Directories are skipped.
func (c *outputCollector) attachSecondaryFiles(schemas []cwlcore.SecondaryFileSchema, value any) error {
	if len(schemas) == 0 {
		return nil
	}

	for _, primary := range outPrimaryFiles(value) {
		err := c.attachToPrimary(primary, schemas)
		if err != nil {
			return err
		}
	}

	return nil
}

// outPrimaryFiles returns the top-level Files in value.
func outPrimaryFiles(value any) []*cwlcore.File {
	items, ok := value.([]any)
	if !ok {
		items = []any{value}
	}

	files := make([]*cwlcore.File, 0, len(items))

	for _, item := range items {
		if file, isFile := item.(*cwlcore.File); isFile {
			files = append(files, file)
		}
	}

	return files
}

// attachToPrimary applies all declared patterns to one primary file.
func (c *outputCollector) attachToPrimary(
	primary *cwlcore.File, schemas []cwlcore.SecondaryFileSchema,
) error {
	secondary := primary.SecondaryFiles
	if secondary == nil {
		secondary = make([]cwlcore.FileOrDirectory, 0, len(schemas))
	}

	for index := range schemas {
		found, err := c.resolveSecondary(primary, &schemas[index])
		if err != nil {
			return err
		}

		secondary = append(secondary, found...)
	}

	primary.SecondaryFiles = secondary

	return nil
}

// resolveSecondary resolves one secondaryFiles pattern against one primary file.
func (c *outputCollector) resolveSecondary(
	primary *cwlcore.File, schema *cwlcore.SecondaryFileSchema,
) ([]cwlcore.FileOrDirectory, error) {
	self := cwlcore.ToExpressionValue(primary)
	pattern := outTrimOptionalMarker(string(schema.Pattern))

	policy, err := c.declaredPolicy(schema, self)
	if err != nil {
		return nil, err
	}

	candidates, err := c.secondaryCandidates(pattern, primary, self)
	if err != nil {
		return nil, err
	}

	return c.secondaryValues(candidates, primary, policy)
}

// secondaryValues resolves candidates to values, dropping missing optional files.
func (c *outputCollector) secondaryValues(
	candidates []any, primary *cwlcore.File, policy outSecondaryPolicy,
) ([]cwlcore.FileOrDirectory, error) {
	found := make([]cwlcore.FileOrDirectory, 0, len(candidates))

	for _, candidate := range candidates {
		// null means no secondary file from this expression.
		if candidate == nil {
			continue
		}

		var err error

		found, err = c.appendSecondary(found, candidate, primary, policy)
		if err != nil {
			return nil, err
		}
	}

	return found, nil
}

// appendSecondary resolves one candidate and appends it if it exists.
func (c *outputCollector) appendSecondary(
	found []cwlcore.FileOrDirectory, candidate any, primary *cwlcore.File, policy outSecondaryPolicy,
) ([]cwlcore.FileOrDirectory, error) {
	local, err := c.secondaryPath(candidate, primary)
	if err != nil {
		return nil, err
	}

	rel := c.relOutPath(local)

	info, statErr := fs.Stat(c.outfs, rel)
	if statErr != nil {
		if policy == outSecondaryRequired {
			return nil, fmt.Errorf("%w: %s", ErrSecondaryMissing, local)
		}

		return found, nil
	}

	value, err := c.secondaryValue(local, info, candidate)
	if err != nil {
		return nil, err
	}

	return append(found, value), nil
}

// outStat returns file info and existence for the given path.
func outStat(local string) (fs.FileInfo, bool) {
	info, err := os.Stat(local)
	if err != nil {
		return nil, false
	}

	return info, true
}

// secondaryPath resolves a candidate to a local filesystem path.
func (c *outputCollector) secondaryPath(candidate any, primary *cwlcore.File) (string, error) {
	switch typed := candidate.(type) {
	case string:
		return filepath.Join(outDirname(primary.Path), typed), nil
	case map[string]any:
		return c.deriveRef(typed).local, nil
	default:
		return "", fmt.Errorf("%w: got %s", ErrSecondaryValue, cwlcore.TypeName(candidate))
	}
}

// secondaryValue builds the File or Directory value for a resolved secondary path.
func (c *outputCollector) secondaryValue(
	local string, info fs.FileInfo, candidate any,
) (cwlcore.FileOrDirectory, error) {
	if object, ok := candidate.(map[string]any); ok {
		return c.retypeEntry(object)
	}

	if info.IsDir() {
		return outNewDirectory(local), nil
	}

	return outMeasureFileFS(local, c.outfs, c.outdir)
}

// secondaryCandidates expands one pattern into candidate filenames or objects.
func (c *outputCollector) secondaryCandidates(
	pattern string, primary *cwlcore.File, self any,
) ([]any, error) {
	if !cwlcore.NeedsParsing(pattern) {
		return []any{outSubstitutePattern(primary.Basename, pattern)}, nil
	}

	value, err := c.eval.Eval(pattern, c.context(self))
	if err != nil {
		return nil, err
	}

	if items, ok := value.([]any); ok {
		return items, nil
	}

	return []any{value}, nil
}

// declaredPolicy evaluates a pattern's `required` field. Default for outputs is optional.
func (c *outputCollector) declaredPolicy(
	schema *cwlcore.SecondaryFileSchema, self any,
) (outSecondaryPolicy, error) {
	switch schema.Required.Kind() {
	case cwlcore.ValueBool:
		return outPolicies[schema.Required.Bool()], nil
	case cwlcore.ValueExpression:
		required, err := c.eval.EvalBool(string(schema.Required.Expression()), c.context(self))
		if err != nil {
			return outSecondaryOptional, err
		}

		return outPolicies[required], nil
	default:
		return outSecondaryOptional, nil
	}
}

// outPolicies maps `required` booleans to policies.
var outPolicies = map[bool]outSecondaryPolicy{
	true:  outSecondaryRequired,
	false: outSecondaryOptional,
}

// outTrimOptionalMarker strips a trailing `?` from a literal pattern.
// Expressions are left alone since `?` may be part of the expression syntax.
func outTrimOptionalMarker(pattern string) string {
	if cwlcore.NeedsParsing(pattern) {
		return pattern
	}

	return strings.TrimSuffix(pattern, "?")
}

// outSubstitutePattern applies caret-extension-stripping and suffix-appending to a basename.
func outSubstitutePattern(basename, pattern string) string {
	name, suffix := basename, pattern

	for strings.HasPrefix(suffix, "^") {
		dot := strings.LastIndexByte(name, '.')
		if dot < 0 {
			return name + strings.TrimLeft(suffix, "^")
		}

		name = name[:dot]
		suffix = suffix[1:]
	}

	return name + suffix
}

// applyFormat evaluates the format expression and records it on every File in value.
func (c *outputCollector) applyFormat(format []cwlcore.Expression, value any) error {
	if len(format) == 0 {
		return nil
	}

	files := outPrimaryFiles(value)
	if len(files) == 0 {
		return c.checkFormatless(format[0], value)
	}

	declared := make([]string, 0, len(files))

	for _, primary := range files {
		iri, err := c.eval.EvalString(string(format[0]), c.context(cwlcore.ToExpressionValue(primary)))
		if err != nil {
			return err
		}

		primary.Format = iri
		declared = append(declared, iri)
	}

	return cwlcore.CheckFormat(cwlcore.ToExpressionValue(value), declared, nil)
}

// checkFormatless validates a format declaration when no File is present in the value.
func (c *outputCollector) checkFormatless(format cwlcore.Expression, value any) error {
	rendered := cwlcore.ToExpressionValue(value)

	iri, err := c.eval.EvalString(string(format), c.context(rendered))
	if err != nil {
		return err
	}

	return cwlcore.CheckFormat(rendered, []string{iri}, nil)
}
