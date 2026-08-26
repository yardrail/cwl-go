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

// secondaryFiles discovery: the second pass over a completed input object.
//
// A declaration's `secondaryFiles` are *patterns*, not values, and applying one needs two things
// that do not exist while a single value is being converted:
//
//   - `self`, bound to the primary File, must already carry every field the specification promises
//     an expression will find on it — basename, nameroot, nameext, location, and the secondary
//     files the invocation itself supplied.
//   - `inputs`, bound to the *whole* input object, because a pattern may be an expression that
//     reads another parameter.
//
// Neither is available until every input has been converted, so discovery is inherently a second
// pass. That is why it lives here rather than inside [joLoader.value].
//
// # Top level versus inside a step
//
// The specification's rule is one thing, but the reference implementation draws a distinction the
// conformance suite depends on, and it is the whole point of the secondary_files_missing test:
//
//   - At the **top level**, a declared pattern is *discovered*. The named companion is looked for
//     on disk and attached to the primary file when it is there; only a required one that is
//     absent is a failure.
//   - **Inside a workflow step**, a declared pattern is *required*. The companion must already be
//     on the File object the step was passed, because it was the enclosing document's job to put
//     it there. Nothing goes to disk, so a companion that happens to exist beside the primary does
//     not silently satisfy the step.
//
// tests/record-in-secondaryFiles-missing-wf.cwl is that distinction in one document: its Workflow
// input declares no secondaryFiles, its step's tool declares `.s2` and `.s3`, and the files are
// sitting on disk next to the primaries. Discovering at the step would find them and the run would
// wrongly succeed.
//
// This file implements the top-level half, which is the half a job order is. A job order is by
// definition the input object of the process a runner was invoked with, so everything reached from
// here is top level — including a bare CommandLineTool run directly, which is most of the suite.
// The step half belongs to whoever builds a step's input object: the values a step is passed come
// from the scheduler (inputs.go / plan_step.go), never through [ParseJobOrder], so the
// already-present check has to be made there or in the CommandLineTool handler that binds them.

// joDiscoverSecondaryFiles applies every declared secondaryFiles pattern to the completed input
// object, attaching what it finds to the primary File each pattern governs.
//
// inputs is mutated in place: the values are [*cwlcore.File] pointers, and discovery adds to their
// SecondaryFiles. Returning a rebuilt object instead would leave two copies of every File, and the
// staging machinery identifies a value by pointer.
func joDiscoverSecondaryFiles(ctx context.Context, inputs map[string]any, p cwlcore.Process) *salad.Error {
	scope := cwlcore.NewScope(p)

	pass := &joSecondaryPass{
		eval: EvaluatorFor(scope),
		// The rendered object is taken once, before anything is attached. A pattern that
		// reads `inputs` therefore sees the invocation as it arrived rather than as it is
		// being decorated, which is the only reading that does not depend on the order the
		// parameters happen to be declared in.
		inputs: outExpressionObject(inputs),
	}

	decls := joDeclaredInputs(p)

	for i := range decls {
		d := &decls[i]

		// Resolving here rather than at each descent is what lets a record type declared by
		// a SchemaDefRequirement carry the field-level patterns inside it: ResolveTypeRef
		// descends arrays, unions and nested fields in one call, and leaves a name it cannot
		// resolve alone. The scope is complete because a job order is loaded against the
		// top-level process, which nothing encloses.
		declared := cwlcore.ResolveTypeRef(scope, d.typ)

		err := pass.value(ctx, inputs[d.name], declared, d.secondary, d.name)
		if err != nil {
			return err
		}
	}

	return nil
}

// joSecondaryPass is the state one discovery pass shares: the evaluator a pattern is evaluated
// with, and the input object it is evaluated against.
type joSecondaryPass struct {
	eval   *cwlcore.Evaluator
	inputs map[string]any
}

// value applies the patterns governing one position to the value at it, descending an array and a
// record so that a File nested in either is reached on exactly the terms a top-level one is.
//
// The descent is driven by the value rather than by the type, because the value is already typed:
// a File is a [*cwlcore.File] whatever union or named type it was declared through. The declared
// type is carried alongside only for what the value cannot say — an array's item declaration and a
// record's field declarations, which is where the nested patterns live.
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

// items applies an array declaration's patterns element by element, which is what Process.yml
// means by "Only valid when `type: File` or is an array of `items: File`".
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

// record descends a record value field by field.
//
// A record carries no patterns of its own: it is not a File, and its Files are its fields' values,
// each of which declares what accompanies it. This mirrors the output side, where a parameter
// whose type reaches a record is collected from the fields' own declarations.
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

// file applies every pattern governing a position to one primary File.
//
// A nil File is skipped rather than dereferenced. It cannot arise from a job order this package
// loaded — an absent optional input is an untyped nil, not a typed one — but the input object is
// public, and a typed nil in it would otherwise be a panic several calls from where it was put
// there.
//
// A File with no path is skipped too. That is a file literal or a resource on another host: there is
// no directory on any filesystem for a companion to sit in, so there is nothing to discover, and
// reporting a required pattern as violated would blame the pattern for the primary's own
// placement.
//
// The field ends up set even when nothing resolved, on the same terms as the output side: an empty
// list records that the patterns were applied and found nothing, which is a different statement
// from the nil that means nobody looked.
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
	self := outFileObject(primary)

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

// policy decides what a missing secondary file means for one pattern.
//
// Process.yml: "When not explicitly specified, secondary files specified for `inputs` are required
// and `outputs` are optional." So the default here is the opposite of the one clt_secondary.go
// applies, and it is the only difference in how the two sides read the same field.
//
// A trailing "?" also makes the pattern optional. The loader normally settles that long before
// this point — salad's secondaryFilesDSL rewrites `".bai?"` into `{pattern: ".bai", required:
// false}` while the document is resolved — but a process built in memory never went through it,
// and reading the marker here means the two spellings cannot disagree.
func (s *joSecondaryPass) policy(
	schema *cwlcore.SecondaryFileSchema, self map[string]any, path string,
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

// evaluatedPolicy evaluates an expression-valued `required`.
//
// A null result means *not* required, which is not the same as the null that means "unspecified"
// on the field itself. The reference implementation reads the evaluated value for its truth and
// None is false there, and the conformance suite pins that reading: filesarray_secondaryfiles runs
// tests/docker-array-secondaryfiles.cwl with `require_dat` unset, so `required:
// $(inputs.require_dat)` evaluates to null against a `.dat2` file that does not exist, and the run
// must still succeed. Falling back to the input-side default of "required" would fail it.
//
// Anything else is an error rather than a coercion, on the same terms as a `when` gate: the schema
// types the field as a boolean, so a value that is not one is a document defect, and guessing at
// its truth would decide whether a run fails on a value nobody meant to write.
func (s *joSecondaryPass) evaluatedPolicy(
	expr cwlcore.Expression, self map[string]any, path string,
) (outSecondaryPolicy, *salad.Error) {
	value, err := s.eval.Eval(string(expr), s.context(self))
	if err != nil {
		return outSecondaryOptional, salad.Errorf(salad.SourceLine{}, "%s: %v", path, err)
	}

	if value == nil {
		return outSecondaryOptional, nil
	}

	required, ok := value.(bool)
	if !ok {
		return outSecondaryOptional, salad.Errorf(salad.SourceLine{},
			"%s: a secondaryFiles `required` must evaluate to a boolean or null, but %s produced %s",
			path, expr, cwlcore.TypeName(value))
	}

	return outPolicies[required], nil
}

// joOptionalMarker reports whether a literal pattern carries the "?" that marks its secondary file
// optional. An expression is never a marker: a "?" ending one belongs to the expression.
func joOptionalMarker(pattern string) bool {
	return !cwlcore.NeedsParsing(pattern) && strings.HasSuffix(pattern, "?")
}

// candidates expands one pattern into the names or objects it produces: a single derived filename
// for a literal pattern, or whatever an expression evaluated to.
//
// Process.yml: the expression "must return a filename string relative to the path to the primary
// File, a File or Directory object ... or an array consisting of strings or File or Directory
// objects as previously described. ... The expression may return 'null' in which case there is no
// secondary file from that expression".
func (s *joSecondaryPass) candidates(
	schema *cwlcore.SecondaryFileSchema, primary *cwlcore.File, self map[string]any, path string,
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

// context builds the environment a secondaryFiles expression is evaluated in.
//
// runtime.* is undefined, for the same reason it is undefined during a step's `when`: the input
// object is settled before any resources are reserved, so there is nothing truthful to bind.
func (s *joSecondaryPass) context(self any) *cwlcore.EvalContext {
	return &cwlcore.EvalContext{Inputs: s.inputs, Self: self}
}

// appendCandidate resolves one candidate and appends the value it names, leaving found untouched
// when the candidate names nothing that exists and the pattern did not require it.
func (s *joSecondaryPass) appendCandidate(
	found []cwlcore.FileOrDirectory,
	candidate any,
	primary *cwlcore.File,
	policy outSecondaryPolicy,
	path string,
) ([]cwlcore.FileOrDirectory, *salad.Error) {
	// A null names no file at all, which is not the same as a named file that is missing, so a
	// required pattern is not violated by one.
	if candidate == nil {
		return found, nil
	}

	ref, err := joCandidateRef(candidate, primary, path)
	if err != nil {
		return nil, err
	}

	// A companion the invocation already supplied is left as it stands. The pattern describes
	// the same file, and re-reading it from disk would both duplicate the entry and discard
	// whatever the job order said about it.
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

// joSecondaryRef is one candidate reduced to what attaching it needs: where the file is, and the
// name it goes by.
type joSecondaryRef struct {
	// local is the filesystem path the candidate names.
	local string

	// name is the basename the attached value carries, which an expression returning an object
	// is allowed to choose.
	name string
}

// joCandidateRef resolves one candidate to the file it names.
//
// A string is "a filename string relative to the path to the primary File", so it resolves against
// the primary's own directory. An object carries its own location or path, which the specification
// explicitly allows to be one "taken from input as a secondaryFile" and so may sit anywhere.
func joCandidateRef(candidate any, primary *cwlcore.File, path string) (joSecondaryRef, *salad.Error) {
	dir := joDirnameOf(primary.Path)

	switch typed := candidate.(type) {
	case string:
		local := joAbsolutize(typed, dir)

		return joSecondaryRef{local: local, name: filepath.Base(local)}, nil
	case map[string]any:
		return joObjectRef(typed, dir), nil
	default:
		return joSecondaryRef{}, salad.Errorf(joNodeLoc(primary.Node),
			"%s: %v: got %s", path, ErrSecondaryValue, cwlcore.TypeName(candidate))
	}
}

// joObjectRef resolves a File or Directory object an expression produced.
//
// Process.yml: "If an expression returns a File object with the same `location` but a different
// `basename` as a secondary file that was passed in, the expression result takes precedence.
// Setting the basename with an expression this way affects the `path` where the secondary file
// will be staged to". So a basename the object supplies is kept rather than re-derived.
func joObjectRef(object map[string]any, dir string) joSecondaryRef {
	local := joSecondaryLocal(outTextField(object, joKeyPath), dir)
	if local == "" {
		local = joSecondaryLocal(outTextField(object, joKeyLocation), dir)
	}

	name := outTextField(object, joKeyBasename)
	if name == "" && local != "" {
		name = filepath.Base(local)
	}

	return joSecondaryRef{local: local, name: name}
}

// joSecondaryLocal resolves a reference an expression produced to a local filesystem path, and ""
// when it names nothing local: an empty reference, or a resource reachable only over some other
// scheme, which is a staging concern rather than a discovery one.
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

	return joAbsolutize(parsed.Path, dir)
}

// joAlreadyPresent reports whether a secondary file of this name is already attached, which is how
// a pattern that merely restates what the invocation supplied is recognised.
func joAlreadyPresent(found []cwlcore.FileOrDirectory, name string) bool {
	for _, value := range found {
		if basenameOf(value) == name {
			return true
		}
	}

	return false
}

// joRenamed puts name on a discovered value, so that a basename an expression chose survives onto
// the value that gets staged. A name derived from the path is what the value already carries, so
// the common case changes nothing.
func joRenamed(value cwlcore.FileOrDirectory, name string) cwlcore.FileOrDirectory {
	if name == "" || basenameOf(value) == name {
		return value
	}

	if file, ok := value.(*cwlcore.File); ok {
		// nameroot and nameext are derived from the basename, so renaming without
		// re-deriving them would leave `nameroot + nameext == basename` unsatisfiable for
		// an expression that reads all three.
		parts := joSplitBasename(name)
		file.Basename, file.Nameroot, file.Nameext = name, parts.root, parts.ext

		return file
	}

	// A Directory has no derived name fields at all: the vendored schema gives it class,
	// location, path, basename and listing and nothing else.
	if dir, ok := value.(*cwlcore.Directory); ok {
		dir.Basename = name
	}

	return value
}

// joArrayItems returns the type an array's elements are declared as, looking through a union so
// that `File[]?` reaches the same item declaration `File[]` does. A type that is not an array
// yields itself, which is what lets a File and an array of File share one descent.
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

// joRecordFields returns the record schema a declared type reaches, looking through a union, and
// nil when it reaches none.
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
