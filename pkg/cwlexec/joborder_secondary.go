package cwlexec

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Top-level secondaryFiles discovery: second pass over a completed input object.
// Patterns may reference `self` and `inputs`, which aren't available until all inputs are loaded.

// joDiscoverSecondaryFiles applies secondaryFiles patterns to the completed input object in place.
func joDiscoverSecondaryFiles(ctx context.Context, inputs map[string]any, p cwlcore.Process) *salad.Error {
	scope := cwlcore.NewScope(p)

	pass := &joSecondaryPass{
		eval:   EvaluatorFor(scope),
		inputs: outExpressionObject(inputs),
	}

	decls := joDeclaredInputs(p)

	for i := range decls {
		d := &decls[i]

		declared := cwlcore.ResolveTypeRef(scope, d.typ)

		err := pass.value(ctx, inputs[d.name], declared, d.secondary, d.name)
		if err != nil {
			return err
		}
	}

	return nil
}

// joSecondaryPass is the shared state for one secondaryFiles discovery pass.
type joSecondaryPass struct {
	eval   *cwlcore.Evaluator
	inputs map[string]any
}

// value applies secondaryFiles patterns to one value, descending into arrays and records.
func (s *joSecondaryPass) value(
	ctx context.Context, value any, typ cwlcore.TypeRef, schemas []cwlcore.SecondaryFileSchema, path string,
) *salad.Error {
	switch typed := value.(type) {
	case *cwlcore.File:
		return s.file(ctx, typed, schemas, path)
	case []any:
		return s.items(ctx, typed, typ, schemas, path)
	case map[string]any:
		return s.record(ctx, typed, typ, path)
	default:
		return nil
	}
}

// items applies patterns element by element over an array.
func (s *joSecondaryPass) items(
	ctx context.Context, values []any, typ cwlcore.TypeRef, schemas []cwlcore.SecondaryFileSchema, path string,
) *salad.Error {
	items := joArrayItems(typ)

	for i, value := range values {
		err := s.value(ctx, value, items, schemas, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return err
		}
	}

	return nil
}

// record descends a record value field by field, applying each field's secondaryFiles patterns.
func (s *joSecondaryPass) record(
	ctx context.Context, values map[string]any, typ cwlcore.TypeRef, path string,
) *salad.Error {
	schema := joRecordFields(typ)
	if schema == nil {
		return nil
	}

	for i := range schema.Fields {
		field := &schema.Fields[i]
		name := ShortName(field.Name)

		err := s.value(ctx, values[name], field.Type, field.SecondaryFiles, path+"."+name)
		if err != nil {
			return err
		}
	}

	return nil
}

// file applies every secondaryFiles pattern to one primary File.
// Skips nil files and files with no local path.
func (s *joSecondaryPass) file(
	ctx context.Context, primary *cwlcore.File, schemas []cwlcore.SecondaryFileSchema, path string,
) *salad.Error {
	if primary == nil || len(schemas) == 0 || primary.Path == "" {
		return nil
	}

	problem := ctx.Err()
	if problem != nil {
		return salad.Errorf(joNodeLoc(primary.Node), "%s: %v", path, problem)
	}

	found := primary.SecondaryFiles
	if found == nil {
		found = make([]cwlcore.FileOrDirectory, 0, len(schemas))
	}

	for i := range schemas {
		next, err := s.pattern(found, primary, &schemas[i], path)
		if err != nil {
			return err
		}

		found = next
	}

	primary.SecondaryFiles = found

	return nil
}

// pattern resolves one declared pattern against one primary file and appends whatever it names.
func (s *joSecondaryPass) pattern(
	found []cwlcore.FileOrDirectory, primary *cwlcore.File, schema *cwlcore.SecondaryFileSchema, path string,
) ([]cwlcore.FileOrDirectory, *salad.Error) {
	self := cwlcore.ToExpressionValue(primary)

	policy, err := s.policy(schema, self, path)
	if err != nil {
		return nil, err
	}

	candidates, err := s.candidates(schema, primary, self, path)
	if err != nil {
		return nil, err
	}

	for _, candidate := range candidates {
		next, appendErr := s.appendCandidate(found, candidate, primary, policy, path)
		if appendErr != nil {
			return nil, appendErr
		}

		found = next
	}

	return found, nil
}

// policy determines whether a missing secondary file is an error. Inputs default to required.
func (s *joSecondaryPass) policy(
	schema *cwlcore.SecondaryFileSchema, self any, path string,
) (outSecondaryPolicy, *salad.Error) {
	switch schema.Required.Kind() {
	case cwlcore.ValueBool:
		return outPolicies[schema.Required.Bool()], nil
	case cwlcore.ValueExpression:
		return s.evaluatedPolicy(schema.Required.Expression(), self, path)
	default:
		return outPolicies[!joOptionalMarker(string(schema.Pattern))], nil
	}
}

// evaluatedPolicy evaluates an expression-valued `required` field. Null means not required.
func (s *joSecondaryPass) evaluatedPolicy(
	expr cwlcore.Expression, self any, path string,
) (outSecondaryPolicy, *salad.Error) {
	value, err := s.eval.Eval(string(expr), s.context(self))
	if err != nil {
		return outSecondaryOptional, salad.Errorf(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"%s: %v",
			path,
			err,
		)
	}

	if value == nil {
		return outSecondaryOptional, nil
	}

	required, ok := value.(bool)
	if !ok {
		return outSecondaryOptional, salad.Errorf(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"%s: a secondaryFiles `required` must evaluate to a boolean or null, but %s produced %s",
			path,
			expr,
			cwlcore.TypeName(value),
		)
	}

	return outPolicies[required], nil
}

// joOptionalMarker reports whether a literal pattern ends with the optional "?" marker.
func joOptionalMarker(pattern string) bool {
	return !cwlcore.NeedsParsing(pattern) && strings.HasSuffix(pattern, "?")
}

// candidates expands one pattern into the filenames or objects it produces.
func (s *joSecondaryPass) candidates(
	schema *cwlcore.SecondaryFileSchema, primary *cwlcore.File, self any, path string,
) ([]any, *salad.Error) {
	pattern := outTrimOptionalMarker(string(schema.Pattern))

	if !cwlcore.NeedsParsing(pattern) {
		return append(make([]any, 0, 1), outSubstitutePattern(primary.Basename, pattern)), nil
	}

	value, err := s.eval.Eval(pattern, s.context(self))
	if err != nil {
		return nil, salad.Errorf(joNodeLoc(primary.Node), "%s: %v", path, err)
	}

	if items, ok := value.([]any); ok {
		return items, nil
	}

	return append(make([]any, 0, 1), value), nil
}

// context builds the evaluation environment for a secondaryFiles expression.
func (s *joSecondaryPass) context(self any) *cwlcore.EvalContext {
	return &cwlcore.EvalContext{
		Inputs: s.inputs,
		Self:   self,
		Runtime: cwlcore.RuntimeContext{
			Cores:      nil,
			RAM:        nil,
			OutdirSize: nil,
			TmpdirSize: nil,
			ExitCode:   nil,
			Outdir:     "",
			Tmpdir:     "",
		},
	}
}

// appendCandidate resolves and appends one candidate if it exists or is required.
func (s *joSecondaryPass) appendCandidate(
	found []cwlcore.FileOrDirectory,
	candidate any,
	primary *cwlcore.File,
	policy outSecondaryPolicy,
	path string,
) ([]cwlcore.FileOrDirectory, *salad.Error) {
	if candidate == nil {
		return found, nil
	}

	ref, err := joCandidateRef(candidate, primary, path)
	if err != nil {
		return nil, err
	}

	if joAlreadyPresent(found, ref.name) {
		return found, nil
	}

	info, present := outStat(ref.local)
	if !present {
		if policy == outSecondaryRequired {
			return nil, salad.Errorf(joNodeLoc(primary.Node),
				"%s: %v: %s", path, ErrSecondaryMissing, ref.name)
		}

		return found, nil
	}

	if info.IsDir() {
		return append(found, joRenamed(outNewDirectory(ref.local), ref.name)), nil
	}

	file, readErr := outMeasureFile(ref.local)
	if readErr != nil {
		return nil, salad.Errorf(joNodeLoc(primary.Node), "%s: %v", path, readErr)
	}

	return append(found, joRenamed(file, ref.name)), nil
}

// joSecondaryRef is a resolved secondary file candidate: path and display name.
type joSecondaryRef struct {
	// local is the filesystem path the candidate names.
	local string

	// name is the basename the attached value carries.
	name string
}

// joCandidateRef resolves one candidate to the file it names.
func joCandidateRef(candidate any, primary *cwlcore.File, path string) (joSecondaryRef, *salad.Error) {
	dir := outDirname(primary.Path)

	switch typed := candidate.(type) {
	case string:
		local := outAbsolutize(typed, dir)

		return joSecondaryRef{local: local, name: filepath.Base(local)}, nil
	case map[string]any:
		return joObjectRef(typed, dir), nil
	default:
		return joSecondaryRef{}, salad.Errorf(joNodeLoc(primary.Node),
			"%s: %v: got %s", path, ErrSecondaryValue, cwlcore.TypeName(candidate))
	}
}

// joObjectRef resolves a File/Directory object produced by an expression.
func joObjectRef(object map[string]any, dir string) joSecondaryRef {
	local := joSecondaryLocal(outTextField(object, outKeyPath), dir)
	if local == "" {
		local = joSecondaryLocal(outTextField(object, outKeyLocation), dir)
	}

	name := outTextField(object, outKeyBasename)
	if name == "" && local != "" {
		name = filepath.Base(local)
	}

	return joSecondaryRef{local: local, name: name}
}

// joSecondaryLocal resolves a reference to a local path, or "" if non-local.
func joSecondaryLocal(ref, dir string) string {
	if ref == "" {
		return ""
	}

	parsed, err := url.Parse(ref)
	if err != nil || parsed.Path == "" {
		return ""
	}

	if parsed.Scheme != "" && parsed.Scheme != joSchemeFile {
		return ""
	}

	return outAbsolutize(parsed.Path, dir)
}

// joAlreadyPresent reports whether a secondary file with this name is already attached.
func joAlreadyPresent(found []cwlcore.FileOrDirectory, name string) bool {
	for _, value := range found {
		if basenameOf(value) == name {
			return true
		}
	}

	return false
}

// joRenamed sets the basename on a discovered value.
func joRenamed(value cwlcore.FileOrDirectory, name string) cwlcore.FileOrDirectory {
	if name == "" || basenameOf(value) == name {
		return value
	}

	if file, ok := value.(*cwlcore.File); ok {
		parts := outSplitName(name)
		file.Basename, file.Nameroot, file.Nameext = name, parts.root, parts.ext

		return file
	}

	if dir, ok := value.(*cwlcore.Directory); ok {
		dir.Basename = name
	}

	return value
}

// joArrayItems returns the item type of an array type, looking through unions.
func joArrayItems(typ cwlcore.TypeRef) cwlcore.TypeRef {
	if schema := typ.Array(); schema != nil {
		return schema.Items
	}

	for _, option := range typ.Options() {
		if schema := option.Array(); schema != nil {
			return schema.Items
		}
	}

	return typ
}

// joRecordFields returns the record schema from a type, looking through unions.
func joRecordFields(typ cwlcore.TypeRef) *cwlcore.RecordSchema {
	if schema := typ.Record(); schema != nil {
		return schema
	}

	for _, option := range typ.Options() {
		if schema := option.Record(); schema != nil {
			return schema
		}
	}

	return nil
}
