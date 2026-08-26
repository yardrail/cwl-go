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

// Secondary files and format, the last two operations of an output binding.
//
// CommandLineTool.yml puts secondaryFiles after outputEval in the order a binding is applied, so
// both run against the *final* value: an outputEval that replaced the globbed files entirely still
// gets its result decorated. The reference implementation applies format last of all, after
// secondaryFiles, and so does this.

// Errors reported while resolving an output parameter's secondaryFiles.
var (
	// ErrSecondaryMissing reports a required secondary file that is not on disk.
	//
	// Process.yml: "An implementation may fail workflow execution if a required secondary file
	// does not exist." It is a failure here, because the alternative is publishing a primary
	// file whose declared companion is absent and letting the tool that consumes it fail
	// instead, several steps away from the cause.
	ErrSecondaryMissing = errors.New("required secondary file does not exist")

	// ErrSecondaryValue reports a secondaryFiles expression that produced something other than a
	// name, a File or Directory object, or an array of those.
	ErrSecondaryValue = errors.New("secondaryFiles expression did not produce a name or a File")
)

// outSecondaryPolicy says what to do about a secondary file that a pattern names but that is not on
// disk.
//
// It is an enumeration rather than a bool because it travels through several calls, and "required"
// read at the call site is worth more than a true nobody can interpret.
type outSecondaryPolicy uint8

const (
	// outSecondaryOptional drops a secondary file that does not exist.
	outSecondaryOptional outSecondaryPolicy = iota

	// outSecondaryRequired reports a secondary file that does not exist.
	outSecondaryRequired
)

// attachSecondaryFiles resolves a declaration's secondaryFiles patterns against every File in its
// collected value. A record field's own patterns reach here on exactly the same terms as a
// parameter's, because both arrive as the [outTarget] projection of the declaration.
//
// Directories are skipped. The vendored schema gives Directory no secondaryFiles field at all, so
// there is nowhere on one to record a companion; the reference implementation attaches them to any
// object it finds, which quietly invents a field.
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

// outPrimaryFiles returns the Files a collected value carries at the top level, which are the ones a
// secondaryFiles or format declaration applies to.
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

// attachToPrimary applies every declared pattern to one primary file.
//
// The field ends up set even when nothing resolved. An empty list records that the patterns were
// applied and found nothing, which is a different statement from the nil that means nobody looked.
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
	self := outFileObject(primary)
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

// secondaryValues turns the candidates one pattern produced into values, dropping the ones that do
// not exist unless the pattern declared them required.
func (c *outputCollector) secondaryValues(
	candidates []any, primary *cwlcore.File, policy outSecondaryPolicy,
) ([]cwlcore.FileOrDirectory, error) {
	found := make([]cwlcore.FileOrDirectory, 0, len(candidates))

	for _, candidate := range candidates {
		// Process.yml: "The expression may return 'null' in which case there is no
		// secondaryFile from that expression." Not a missing file — no file was named at all,
		// so a required pattern is not violated by one.
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

// appendSecondary resolves one candidate and appends the value it names to found, leaving found
// untouched when the candidate names nothing that exists and the pattern did not require it.
//
// Appending here rather than in the caller is what keeps "this one is missing and that is fine"
// from having to travel back as a nil value, which is indistinguishable from a bug.
func (c *outputCollector) appendSecondary(
	found []cwlcore.FileOrDirectory, candidate any, primary *cwlcore.File, policy outSecondaryPolicy,
) ([]cwlcore.FileOrDirectory, error) {
	local, err := c.secondaryPath(candidate, primary)
	if err != nil {
		return nil, err
	}

	info, present := outStat(local)
	if !present {
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

// outStat reports the file information for local, and whether there is anything there at all. The
// reason for a boolean rather than an error is that "not there" is an ordinary answer for a
// secondary file rather than a failure, and the caller is what decides which it is.
func outStat(local string) (fs.FileInfo, bool) {
	info, err := os.Stat(local)
	if err != nil {
		return nil, false
	}

	return info, true
}

// secondaryPath resolves one candidate to the local path it names.
//
// A name is relative to the primary file's own directory, which is what Process.yml means by "a
// filename relative to the path to the primary File". An object carries its own location, and an
// expression is allowed to return one taken unchanged from the input object, so it may name
// something well outside the output directory.
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

// secondaryValue builds the value for the secondary file at local, which is known to exist.
//
// When the pattern produced an object rather than a name, the value is built from that object, so
// that a File taken unchanged from the input object keeps whatever it already carried.
func (c *outputCollector) secondaryValue(
	local string, info fs.FileInfo, candidate any,
) (cwlcore.FileOrDirectory, error) {
	if object, ok := candidate.(map[string]any); ok {
		return c.retypeEntry(object)
	}

	if info.IsDir() {
		return outNewDirectory(local), nil
	}

	return outMeasureFile(local)
}

// secondaryCandidates expands one pattern into the candidates it names: a single derived filename
// for a literal pattern, or whatever an expression produced.
//
// Process.yml: the expression "must return a filename string relative to the path to the primary
// File, a File or Directory object with either `path` or `location` and `basename` fields set, or
// an array consisting of strings or File or Directory objects. ... The expression may return 'null'
// in which case there is no secondaryFile from that expression".
func (c *outputCollector) secondaryCandidates(
	pattern string, primary *cwlcore.File, self map[string]any,
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

// declaredPolicy evaluates a pattern's `required` field, which may itself be an expression with
// self bound to the primary file.
//
// Process.yml: "When not explicitly specified, secondary files specified for `inputs` are required
// and `outputs` are optional." These are outputs, so an undeclared field is optional.
func (c *outputCollector) declaredPolicy(
	schema *cwlcore.SecondaryFileSchema, self map[string]any,
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

// outPolicies is the policy each declared `required` boolean selects. It is a lookup rather than an
// if, so that no boolean has to travel as a control-flow parameter.
var outPolicies = map[bool]outSecondaryPolicy{
	true:  outSecondaryRequired,
	false: outSecondaryOptional,
}

// outTrimOptionalMarker applies the first of the specification's three pattern rules: "If string ends
// with `?` character, remove the last `?` and mark the resulting secondary file as optional."
//
// Only the trimming has an effect here, and it is the half that matters: a `?` left on the pattern
// would go into the filename rule below and look for "reads.bai?" on disk. The marking is already
// the default an output gets, so the only way it could change anything is against an explicit
// `required: true`, and an explicit declaration outranks a shorthand.
//
// The rule applies only to a literal pattern. A `?` at the end of an expression belongs to the
// expression — a conditional operator, most likely — and is not a marker.
func outTrimOptionalMarker(pattern string) string {
	if cwlcore.NeedsParsing(pattern) {
		return pattern
	}

	return strings.TrimSuffix(pattern, "?")
}

// outSubstitutePattern applies the other two of the specification's pattern rules to a primary file's
// basename: "2. If string begins with one or more caret `^` characters, for each caret, remove the
// last file extension from the path (the last period `.` and all following characters). If there
// are no file extensions, the path is unchanged. 3. Append the remainder of the string to the end
// of the file path."
//
// Note what rule 2 does when the carets run out of extensions to strip: the name is left as it is
// and the unspent carets are dropped, so "^^.idx" gives "reads.idx" from both "reads.bam" and
// "reads". Stripping is by the *last* period, so ".cshrc" with "^.bak" gives ".bak" — the leading
// period is an extension boundary here, unlike in the nameroot/nameext split, because rule 2 says
// "the last period" without the exception Process.yml makes for nameroot.
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

// applyFormat evaluates a declaration's format and records it on every File in its value.
//
// Process.yml, OutputFormat: an output's format is "one or more IRIs of concept nodes that
// represents file formats" the parameter produces. It declares what came out rather than
// constraining what may go in, so the evaluated IRI is written onto the value. Having written it,
// the value goes through [cwlcore.CheckFormat], which is where every judgement about a format in
// this project lives — and the check is not vacuous, because it is what rejects a format declared
// on an output whose value turns out not to be a File at all.
//
// The schema types an output's format as `string | Expression`, so format holds at most one entry
// whether it came from a parameter or from a record field.
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
		iri, err := c.eval.EvalString(string(format[0]), c.context(outFileObject(primary)))
		if err != nil {
			return err
		}

		primary.Format = iri
		declared = append(declared, iri)
	}

	return cwlcore.CheckFormat(outExpressionValue(value), declared, nil)
}

// checkFormatless reports a format declared on an output whose value holds no File to carry it. A
// null value — an optional output that collected nothing — is fine, and cwlcore.CheckFormat is what
// says which of the two this is.
func (c *outputCollector) checkFormatless(format cwlcore.Expression, value any) error {
	rendered := outExpressionValue(value)

	iri, err := c.eval.EvalString(string(format), c.context(rendered))
	if err != nil {
		return err
	}

	return cwlcore.CheckFormat(rendered, []string{iri}, nil)
}
