package cwlexec

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Job-order loading: turning the *input object* a runner is invoked with into the fully
// normalised, typed value map a process executes against.
//
// This lives in cwlexec rather than cwlcore because it does I/O. Normalising a File means
// stat-ing it, hashing it and resolving its location against a base directory; cwlcore is a
// pure typed-model layer that deliberately never touches a filesystem, and its own doc comment
// on [cwlcore.File] says so: "Populating them is the runner's job".
//
// Three things happen here, in this order, and the order matters:
//
//  1. The job document is parsed with [salad.Parse], so YAML and JSON take the same path and
//     every diagnostic carries an accurate source line.
//  2. Declared inputs the job omits fall back to their `default`, and an omitted input with no
//     default whose type does not accept null is an error naming the parameter. This mirrors
//     cwltool's fill_in_defaults, including its treatment of an explicit `null` as "absent".
//  3. Every value is type-checked against its declared type and normalised — which for a File or
//     Directory means resolving location/path, deriving basename/dirname/nameroot/nameext, and
//     computing size and checksum from disk.
//  4. A second pass applies each declaration's `secondaryFiles` patterns to the object the third
//     step finished building, which is the earliest point at which a pattern that is an expression
//     over `inputs` can be evaluated. See joborder_secondary.go.
//
// What deliberately does *not* happen here is described on [LoadJobOrder]: staging a file literal
// onto a disk is an execution-time concern.

// joMaxContentsBytes is the ceiling the specification places on a file literal's `contents`, and
// on a file read by `loadContents`.
//
// Process.yml, File: "The maximum size of `contents` is 64 kilobytes." And on loadContents: "the
// file ... must be a UTF-8 text file 64 KiB or smaller ... If the size of the file is greater
// than 64 KiB, the implementation must raise a fatal error." The two spellings ("64 kilobytes",
// "64 KiB") describe the same limit; it is measured in bytes, not in runes, because that is what
// a file's size is.
const joMaxContentsBytes = 64 * 1024

// joSchemeFile is the IRI scheme every conforming implementation must support for a File or
// Directory location.
const joSchemeFile = "file"

// LoadJobOrder reads the job order at jobPath and normalises it into the input object p runs
// against.
//
// The returned map is keyed by the *short* name of each declared input — the part of the
// parameter identifier after the last '#' and '/' — which is the spelling a job file uses and
// the spelling `inputs.<name>` uses inside a CWL expression. Every declared input appears as a
// key, including an optional one the job omitted, whose value is a nil `any`: an expression
// referring to it must see null rather than an undefined symbol.
//
// Values are typed, not raw maps. A File is a [*cwlcore.File] and a Directory a
// [*cwlcore.Directory], because that representation is canonical across the engine — the
// expression evaluator, [cwlcore.FormatOntology] checks and the CommandLineTool handler all
// consume it. Everything else is a plain Go value: nil, bool, int64, float64, string, []any, or
// map[string]any for a record.
//
// Relative `location` and `path` references resolve against **the job file's directory**, not
// the process working directory, per the specification's rule that a relative location "must be
// resolved relative to the IRI of the document it appears in". A value that comes from a
// parameter's `default` appears in the *process* document instead, so it resolves against the
// process document's directory; see [ParseJobOrder].
//
// A Directory value's `listing` is materialised here, under the precedence Process.yml gives
// `loadListing`: the declaring parameter's or record field's own setting, then a
// LoadListingRequirement in scope on the process, then the `no_listing` default. Under
// `no_listing` the listing stays nil, which means "nobody read it" rather than "the directory is
// empty" — see [cwlcore.Directory.Listing].
//
// A declared parameter's or record field's `secondaryFiles` patterns are applied here too, in a
// second pass over the finished object — a pattern may be an expression reading `inputs`, so it
// cannot run any earlier. Discovery is the *top-level* rule: a named companion is looked for on
// disk, and only a required one that is absent is a failure. The step-level rule, where a declared
// companion must already be present on the value the step was handed, belongs to whoever builds a
// step's input object; see [joDiscoverSecondaryFiles].
//
// One thing this deliberately leaves to execution time:
//
//   - File literals. A File carrying `contents` and no location is carried through with its
//     contents, size and checksum set, but nothing is written to disk: the specification says a
//     literal is "created on disk with `contents` when needed for executing a tool".
//
// jobPath may be relative; it is resolved against the process working directory before anything
// else happens, so that the base every reference resolves against is fixed at entry.
//
// ctx is observed at each filesystem value, so a cancelled context stops a job order that names
// a great many files rather than hashing all of them.
func LoadJobOrder(
	ctx context.Context, jobPath string, p cwlcore.Process, opts ...JobOrderOption,
) (map[string]any, error) {
	// Making the path absolute and reading it are one step with one failure, because they
	// fail together: filepath.Abs consults the working directory and can only fail when that
	// directory has been removed, in which case reading a relative path fails too. Clean
	// marks the argument as sanitized for gosec's taint analysis and changes nothing else.
	abs, absErr := filepath.Abs(jobPath)
	src, readErr := os.ReadFile(filepath.Clean(jobPath))

	problem := errors.Join(absErr, readErr)
	if problem != nil {
		return nil, salad.Errorf(salad.SourceLine{File: jobPath}, "reading job order: %v", problem)
	}

	return ParseJobOrder(ctx, abs, src, p, opts...)
}

// JobOrderOption configures how a job order is loaded.
type JobOrderOption func(*joLoader)

// WithJobOrderLogger routes the advisories loading reports — an undeclared key, most of all — to
// log rather than to [slog.Default].
//
// It exists because those advisories are otherwise unreachable. A caller that writes its own
// diagnostics to a chosen stream, as cwl-run does so that --quiet can silence them, never sets
// the default logger; the warning was therefore written to a stream nobody was reading, and no
// flag could suppress it. An advisory the user cannot see is not an advisory.
func WithJobOrderLogger(log *slog.Logger) JobOrderOption {
	return func(l *joLoader) { l.log = log }
}

// ParseJobOrder normalises an in-memory job document, and is what [LoadJobOrder] is built from.
//
// jobPath need not exist, but it must be absolute. It supplies two things: the file name every
// diagnostic is reported against, and — through its directory — the base that relative
// `location` and `path` references resolve against. Requiring it to be absolute is what makes
// the resolution base explicit rather than dependent on the process working directory, which is
// the whole point of the parameter; [LoadJobOrder] resolves a relative path for you.
//
// A process with no job order at all — every input optional or defaulted — is loaded by passing
// an empty mapping:
//
//	inputs, err := cwlexec.ParseJobOrder(ctx, filepath.Join(cwd, "-"), []byte("{}"), tool)
//
// A parameter's `default` is a value written in the *process* document, so a relative reference
// inside one resolves against that document's directory rather than the job file's. Which
// document that is comes from [joProcessFile]; when nothing about the process names one — a
// process built in memory, or one loaded from a remote URL — the job file's directory is used,
// since there is nothing better available.
//
// A key in the job object that names no declared input is ignored, and reported as a warning
// through [slog.Default]. It was previously an error, on the reasoning that a misspelled input
// name is otherwise completely silent for an optional parameter — but the conformance suite
// settles it the other way: nested_prefixes_arrays runs tests/binding-test.cwl against
// tests/bwa-mem-job.json, which carries `min_std_max_min` and `minimum_seed_length` for a tool
// that declares neither, and expects the run to succeed. The reference implementation ignores
// extra keys, so the diagnostic moves to the log rather than disappearing.
//
// Three shapes are not even worth a warning, since a job file legitimately carries them: `id`,
// any key beginning with '$' ($namespaces, $schemas), and any key containing ':', which is a
// namespaced extension key such as `cwl:tool` or `cwl:requirements`.
//
// One of those, `cwl:requirements`, is not merely tolerated: it is the specification's optional
// input-object requirements merge, and its entries are appended to p's own requirements before the
// job order is read. **This modifies p**, which is the only way the merged requirement can reach
// everything that has to honour it — the execution environment, a nested step that inherits it, and
// the load below, which consults a LoadListingRequirement. See [joMergeRequirements] for the
// precedence and for why appending is what implements it.
func ParseJobOrder(
	ctx context.Context, jobPath string, src []byte, p cwlcore.Process, opts ...JobOrderOption,
) (map[string]any, error) {
	if p == nil {
		return nil, salad.Errorf(salad.SourceLine{File: jobPath},
			"a job order must be loaded against a process, but none was given")
	}

	if !filepath.IsAbs(jobPath) {
		return nil, salad.Errorf(salad.SourceLine{File: jobPath},
			"the job order path %q must be absolute, since it is what relative references resolve against", jobPath)
	}

	root, err := salad.Parse(jobPath, src)
	if err != nil {
		return nil, err
	}

	// Before anything reads a requirement off p, since a LoadListingRequirement or a
	// SchemaDefRequirement supplied by the input object has to be in effect for the very load
	// this merge precedes.
	merged := joMergeRequirements(root, p)
	if merged != nil {
		return nil, merged
	}

	jobDir := filepath.Dir(jobPath)
	listing, _ := loadListingDefault(cwlcore.NewScope(p))

	loader := &joLoader{
		jobDir:  jobDir,
		docDir:  joProcessDir(p, jobDir),
		vocab:   joReadVocabulary(p, root),
		listing: listing,
	}

	for _, opt := range opts {
		opt(loader)
	}

	inputs, jobErr := loader.load(ctx, root, p)
	if jobErr != nil {
		return nil, jobErr
	}

	// A second pass, and it has to be: a secondaryFiles pattern may be an expression whose
	// `inputs` is the object the first pass has only just finished building. See
	// [joDiscoverSecondaryFiles].
	found := joDiscoverSecondaryFiles(ctx, inputs, p)
	if found != nil {
		return nil, found
	}

	return inputs, nil
}

// joLoader carries everything a job order is read against that a single value cannot work out for
// itself: the two base directories references resolve against, the vocabulary a `format` is
// written in, and the listing depth the process's requirements ask for.
//
// It holds no context: cancellation is threaded through the call chain instead, so that the
// loader stays safe to reuse and the linter's contained-context rule stays satisfied.
type joLoader struct {
	// vocab is the linked-data view of the process document: the prefix table a `format`
	// expands against, and whether an ontology is available to reason about one.
	vocab joVocabulary

	// log receives the diagnostics that are reported but not fatal. Nil means
	// [slog.Default]; reach it through [joLoader.logger].
	log *slog.Logger

	// jobDir is the absolute directory of the job document. Relative references in the job
	// object resolve against it.
	jobDir string

	// docDir is the absolute directory of the process document. Relative references inside a
	// parameter's `default` resolve against it.
	docDir string

	// listing is the LoadListingRequirement in effect on the process, which is the second step
	// of the `loadListing` precedence and so the default a parameter that sets none inherits.
	listing cwlcore.LoadListingEnum
}

// logger returns the loader's logger, or [slog.Default] when it has none, so that a diagnostic is
// never silently dropped for want of configuration.
func (l *joLoader) logger() *slog.Logger {
	if l.log == nil {
		return slog.Default()
	}

	return l.log
}

// load walks p's declared inputs in document order and builds the input object.
//
// Errors from every input are collected rather than the first one returned, because a job order
// with three wrong values should say so once. They are grouped under a single parent so that
// [salad.Error.Pretty] renders them as a tree and [salad.Error.Leaves] yields exactly the tips.
func (l *joLoader) load(ctx context.Context, root salad.Node, p cwlcore.Process) (map[string]any, *salad.Error) {
	supplied, ok := salad.AsMap(root)
	if !ok {
		return nil, salad.Errorf(joNodeLoc(root),
			"a job order must be a mapping of input names to values, but this is %s", salad.NodeKind(root))
	}

	decls := joDeclaredInputs(p)

	names := make([]string, 0, len(decls))
	for i := range decls {
		names = append(names, decls[i].name)
	}

	l.warnUndeclared(supplied, names)

	problems := make([]*salad.Error, 0, len(decls))
	values := make(map[string]any, len(decls))

	for i := range decls {
		d := &decls[i]

		value, err := l.input(ctx, d, supplied)
		if err != nil {
			problems = append(problems, err)

			continue
		}

		values[d.name] = value
	}

	if len(problems) > 0 {
		return nil, salad.Group(joNodeLoc(root), "the job order is not valid for this process", problems...)
	}

	return values, nil
}

// warnUndeclared logs the keys of the job object that name no declared input.
//
// It is a warning rather than a failure only because the conformance suite requires it; see
// [ParseJobOrder]. The warning is what is left of the diagnostic, and it matters: an undeclared
// key is most often a misspelling, and the parameter it was meant for is now quietly unset.
func (l *joLoader) warnUndeclared(supplied *salad.MapNode, declared []string) {
	undeclared := joUndeclaredKeys(supplied, declared)
	if len(undeclared) == 0 {
		return
	}

	l.logger().Warn("ignoring job order keys that name no declared input",
		slog.String("file", supplied.Loc().File),
		slog.String("ignored", strings.Join(undeclared, ", ")),
		slog.String("declared", strings.Join(declared, ", ")))
}

// input resolves one declared input: the supplied value, else the parameter's default, else
// null when the type permits it, else an error naming the parameter.
//
// An explicit `null` in the job object counts as absent, so that writing `in: null` selects the
// default rather than defeating it. This matches cwltool's fill_in_defaults, which tests
// `job.get(name) is None` rather than key presence.
func (l *joLoader) input(ctx context.Context, d *joInput, supplied *salad.MapNode) (any, *salad.Error) {
	value := &joValueCtx{
		typ:          d.typ,
		base:         l.jobDir,
		path:         d.name,
		format:       d.format,
		loadContents: d.loadContents,
		listing:      cmp.Or(d.listing, l.listing),
	}

	if node, ok := supplied.Get(d.name); ok && !salad.IsNull(node) {
		return l.value(ctx, node, value)
	}

	if !salad.IsNull(d.def) {
		value.base = l.docDir

		return l.value(ctx, d.def, value)
	}

	if d.typ.IsOptional() || d.typ.IsNull() {
		return nil, nil
	}

	return nil, salad.Errorf(
		joNodeLoc(d.node),
		"input %q: the job order supplies no value, the parameter declares no default, and its type %s does not accept null",
		d.name,
		d.typ,
	)
}

// joInput is one declared input parameter, flattened out of the five per-class parameter
// types into the handful of fields job-order loading actually needs.
type joInput struct {
	// node is the parameter's source node, for diagnostics. Nil for a hand-built process.
	node salad.Node

	// def is the parameter's `default`, kept as the salad node the model carries it as. Nil
	// when the parameter declares none.
	def salad.Node

	// name is the short name the job object keys this input by.
	name string

	// format lists the IRIs a File value bound here may declare, empty when the parameter
	// constrains nothing.
	format []string

	// secondary are the parameter's secondaryFiles patterns, applied by the second pass.
	secondary []cwlcore.SecondaryFileSchema

	// typ is the declared type.
	typ cwlcore.TypeRef

	// listing is the parameter's own `loadListing`, the first step of the precedence. Empty
	// means it declares none and inherits.
	listing cwlcore.LoadListingEnum

	// loadContents requests that a File value's contents be read from disk.
	loadContents bool
}

// joDeclaredInputs flattens a process's declared inputs into a uniform slice, in document order.
//
// The five process classes carry three different parameter types between them — an
// ExpressionTool reuses the Workflow parameter, and a RawProcess reuses the Operation one, as
// the schema itself does — so three small converters cover all five.
func joDeclaredInputs(p cwlcore.Process) []joInput {
	switch proc := p.(type) {
	case *cwlcore.CommandLineTool:
		return joCommandInputs(proc.Inputs)
	case *cwlcore.Workflow:
		return joWorkflowInputs(proc.Inputs)
	case *cwlcore.ExpressionTool:
		return joWorkflowInputs(proc.Inputs)
	case *cwlcore.Operation:
		return joOperationInputs(proc.Inputs)
	case *cwlcore.RawProcess:
		return joOperationInputs(proc.Inputs)
	default:
		// Process is sealed, so this is unreachable for a value built by cwlcore; it
		// exists so that a future process class fails as an empty input set rather than
		// a panic.
		return make([]joInput, 0)
	}
}

// joCommandInputs converts a CommandLineTool's inputs.
func joCommandInputs(params []cwlcore.CommandInputParameter) []joInput {
	decls := make([]joInput, 0, len(params))
	for i := range params {
		decls = append(decls, joInputOf(&params[i].ParameterBase, params[i].Default))
	}

	return decls
}

// joWorkflowInputs converts a Workflow's or an ExpressionTool's inputs.
func joWorkflowInputs(params []cwlcore.WorkflowInputParameter) []joInput {
	decls := make([]joInput, 0, len(params))
	for i := range params {
		decls = append(decls, joInputOf(&params[i].ParameterBase, params[i].Default))
	}

	return decls
}

// joOperationInputs converts an Operation's or a RawProcess's inputs.
func joOperationInputs(params []cwlcore.OperationInputParameter) []joInput {
	decls := make([]joInput, 0, len(params))
	for i := range params {
		decls = append(decls, joInputOf(&params[i].ParameterBase, params[i].Default))
	}

	return decls
}

// joInputOf builds a joInput from the shared parameter base and the class-specific default.
func joInputOf(base *cwlcore.ParameterBase, def salad.Node) joInput {
	return joInput{
		node:         base.Node,
		def:          def,
		name:         ShortName(base.IDField),
		format:       joAllowedFormats(base.Format),
		secondary:    base.SecondaryFiles,
		typ:          base.Type,
		listing:      base.LoadListing,
		loadContents: base.LoadContents,
	}
}

// joProcessDir returns the directory a relative reference inside a parameter's `default`
// resolves against: the directory of the process document, recovered from the process
// identifier. fallback is used when the identifier names no document — a blank node, or a
// process built in memory.
func joProcessDir(p cwlcore.Process, fallback string) string {
	local := joProcessFile(p)
	if local == "" {
		return fallback
	}

	return path.Dir(local)
}

// joProcessFile returns the local path of the document p was decoded from, and "" when nothing
// about p names a local document: a process built in memory, or one loaded from a remote URL.
//
// Two sources, in order. The process identifier is the direct one, but it is often not one at
// all: the schema makes `id` optional, and decoding gives a process that declares none a blank
// node of the form "_:<uuid>". Most of the conformance suite's tools are exactly that, so relying
// on the identifier alone would find the document only for the minority that name themselves.
//
// The fallback is the source location recorded on a declared input's node, which is the document
// that parameter was parsed out of — the same document, by construction. A process with no inputs
// falls through with nothing, which costs nothing: with no parameters there is no `format` to
// expand, no `default` to resolve and no Directory to list.
func joProcessFile(p cwlcore.Process) string {
	local := joLocalPath(p.Base().ID)
	if local != "" {
		return local
	}

	decls := joDeclaredInputs(p)
	for i := range decls {
		node := decls[i].node
		if node == nil {
			continue
		}

		local = joLocalPath(node.Loc().File)
		if local != "" {
			return local
		}
	}

	return ""
}

// joLocalPath returns the local filesystem path a document reference names, and "" when it names
// none: an empty reference, a blank node, or a document on another host.
//
// The fragment is dropped first, because a reference addressing one object inside a document —
// the "pack.cwl#main" form — still names the document as a whole.
func joLocalPath(ref string) string {
	ref, _, _ = strings.Cut(ref, "#")

	if strings.HasPrefix(ref, "/") {
		return ref
	}

	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme != joSchemeFile || parsed.Path == "" {
		return ""
	}

	return parsed.Path
}

// joReservedKey reports whether a key in a job object or a filesystem value is exempt from the
// unknown-key check: an identifier, a salad directive, or a namespaced extension key.
func joReservedKey(key string) bool {
	return key == "id" || strings.HasPrefix(key, "$") || strings.Contains(key, ":")
}

// joCheckKeys reports every key of m that is neither reserved nor a member of allowed. what names
// the kind of thing being checked, for the message.
func joCheckKeys(m *salad.MapNode, allowed []string, what string) *salad.Error {
	problems := make([]*salad.Error, 0, m.Len())

	for _, entry := range m.Entries() {
		if joReservedKey(entry.Key) || slices.Contains(allowed, entry.Key) {
			continue
		}

		problems = append(problems, salad.Errorf(joNodeLoc(entry.Value),
			"%q is not a declared %s; expected one of %s", entry.Key, what, joJoinQuoted(allowed)))
	}

	if len(problems) == 0 {
		return nil
	}

	return salad.Group(m.Loc(), "unrecognized "+what, problems...)
}

// joUndeclaredKeys returns every key of m that is neither reserved nor a member of allowed, in
// document order. It is [joCheckKeys] without the verdict, for the one caller that reports rather
// than rejects.
func joUndeclaredKeys(m *salad.MapNode, allowed []string) []string {
	keys := make([]string, 0, m.Len())

	for _, key := range m.Keys() {
		if joReservedKey(key) || slices.Contains(allowed, key) {
			continue
		}

		keys = append(keys, key)
	}

	return keys
}

// joJoinQuoted renders names as a comma-separated list of quoted names, or "none" when empty.
func joJoinQuoted(names []string) string {
	if len(names) == 0 {
		return "none"
	}

	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}

	return strings.Join(quoted, ", ")
}

// joNodeLoc returns n's source location, tolerating a nil node.
func joNodeLoc(n salad.Node) salad.SourceLine {
	if n == nil {
		return salad.SourceLine{}
	}

	return n.Loc()
}
