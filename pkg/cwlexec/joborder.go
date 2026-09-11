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

// Job-order loading: turns the input object into a normalised, typed value map.
// Lives in cwlexec (not cwlcore) because it does filesystem I/O.

// joMaxContentsBytes is the CWL spec limit on file literal contents and loadContents (64 KiB).
const joMaxContentsBytes = 64 * 1024

// joSchemeFile is the IRI scheme for File/Directory locations.
const joSchemeFile = "file"

// LoadJobOrder reads and normalises the job order at jobPath into the typed input map for p.
// Relative references resolve against the job file's directory. File literals are not staged to disk.
func LoadJobOrder(
	ctx context.Context, jobPath string, p cwlcore.Process, opts ...JobOrderOption,
) (map[string]any, error) {
	abs, absErr := filepath.Abs(jobPath)
	src, readErr := os.ReadFile(filepath.Clean(jobPath))

	problem := errors.Join(absErr, readErr)
	if problem != nil {
		return nil, salad.Errorf(
			salad.SourceLine{
				File:  jobPath,
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"reading job order: %v",
			problem,
		)
	}

	return ParseJobOrder(ctx, abs, src, p, opts...)
}

// JobOrderOption configures how a job order is loaded.
type JobOrderOption func(*joLoader)

// WithJobOrderLogger routes load-time advisories to log instead of [slog.Default].
func WithJobOrderLogger(log *slog.Logger) JobOrderOption {
	return func(l *joLoader) { l.log = log }
}

// ParseJobOrder normalises an in-memory job document against process p.
// jobPath must be absolute; it sets the base for relative reference resolution.
// Undeclared keys are warned, not rejected. cwl:requirements entries are merged into p.
func ParseJobOrder(
	ctx context.Context, jobPath string, src []byte, p cwlcore.Process, opts ...JobOrderOption,
) (map[string]any, error) {
	if p == nil {
		return nil, salad.Errorf(
			salad.SourceLine{
				File:  jobPath,
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"a job order must be loaded against a process, but none was given",
		)
	}

	if !filepath.IsAbs(jobPath) {
		return nil, salad.Errorf(
			salad.SourceLine{
				File:  jobPath,
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"the job order path %q must be absolute, since it is what relative references resolve against",
			jobPath,
		)
	}

	root, err := salad.Parse(jobPath, src)
	if err != nil {
		return nil, err
	}

	// Merge cwl:requirements before anything reads requirements from p.
	merged := joMergeRequirements(root, p)
	if merged != nil {
		return nil, merged
	}

	jobDir := filepath.Dir(jobPath)
	listing, _ := loadListingDefault(cwlcore.NewScope(p))

	loader := &joLoader{
		vocab:   joReadVocabulary(p, root),
		log:     nil,
		jobDir:  jobDir,
		docDir:  joProcessDir(p, jobDir),
		listing: listing,
	}

	for _, opt := range opts {
		opt(loader)
	}

	inputs, jobErr := loader.load(ctx, root, p)
	if jobErr != nil {
		return nil, jobErr
	}

	// Second pass: secondaryFiles patterns may reference `inputs` from the first pass.
	found := joDiscoverSecondaryFiles(ctx, inputs, p)
	if found != nil {
		return nil, found
	}

	return inputs, nil
}

// joLoader carries the shared state for loading a job order.
type joLoader struct {
	// vocab is the prefix table for expanding `format` IRIs.
	vocab joVocabulary

	// log receives non-fatal diagnostics. Nil means [slog.Default].
	log *slog.Logger

	// jobDir is the base for relative references in the job object.
	jobDir string

	// docDir is the base for relative references in parameter defaults.
	docDir string

	// listing is the process-level LoadListingRequirement default.
	listing cwlcore.LoadListingEnum
}

// logger returns the loader's logger, defaulting to [slog.Default].
func (l *joLoader) logger() *slog.Logger {
	if l.log == nil {
		return slog.Default()
	}

	return l.log
}

// load builds the input object from p's declared inputs, collecting all errors.
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

// warnUndeclared logs job object keys that name no declared input.
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

// input resolves one declared input: supplied value, then default, then null if allowed.
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

// joInput is a declared input parameter, flattened for job-order loading.
type joInput struct {
	// node is the parameter's source node for diagnostics.
	node salad.Node

	// def is the parameter's `default` value. Nil when none declared.
	def salad.Node

	// name is the short name the job object keys this input by.
	name string

	// format lists the allowed format IRIs, empty when unconstrained.
	format []string

	// secondary are the parameter's secondaryFiles patterns, applied by the second pass.
	secondary []cwlcore.SecondaryFileSchema

	// typ is the declared type.
	typ cwlcore.TypeRef

	// listing is the parameter's own `loadListing`. Empty means inherit.
	listing cwlcore.LoadListingEnum

	// loadContents requests that a File value's contents be read from disk.
	loadContents bool
}

// joDeclaredInputs flattens a process's declared inputs into a uniform slice.
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

// joProcessDir returns the process document's directory, or fallback if unknown.
func joProcessDir(p cwlcore.Process, fallback string) string {
	local := joProcessFile(p)
	if local == "" {
		return fallback
	}

	return path.Dir(local)
}

// joProcessFile returns the local path of p's source document, or "" if unknown.
// Falls back to the source location of the first declared input.
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

// joLocalPath extracts a local filesystem path from a document reference, or "" if non-local.
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

// joReservedKey reports whether a key is exempt from unknown-key checks.
func joReservedKey(key string) bool {
	return key == "id" || strings.HasPrefix(key, "$") || strings.Contains(key, ":")
}

// joCheckKeys reports keys in m that are neither reserved nor in allowed.
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

// joUndeclaredKeys returns keys in m that are neither reserved nor in allowed.
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
		return salad.SourceLine{
			File:  "",
			Start: salad.Position{Line: 0, Column: 0, Offset: 0},
			End:   salad.Position{Line: 0, Column: 0, Offset: 0},
		}
	}

	return n.Loc()
}
