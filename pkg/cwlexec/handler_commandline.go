package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The built-in CommandLineTool handler: the one place the pieces meet.
//
// Everything it does is already written somewhere else — BuildCommandLine makes the argv,
// StageInitialWorkDir and PathMap fill the working directory, RunProcess spawns the program,
// ClassifyExit reads the exit code, CollectOutputs reads the directory. What lives here is the
// order they go in, and the handful of decisions that only make sense once they are all present.

// ErrUnsupportedFeature reports a CWL feature this engine recognizes and has not implemented.
//
// It exists so that "we cannot do this" is distinguishable from "your document is wrong", which
// matters at exactly one boundary: cmd/cwl-run maps it to exit status 33, the cwl-runner contract's
// code for a tool that "could not be run because a feature is unsupported", and the cwltest harness
// reads that as a skip rather than a failure. Test for it with [errors.Is].
//
// What it covers today: a DockerRequirement in requirements, since container execution is not
// implemented; an InitialWorkDirRequirement entryname that is an absolute path, which the
// specification permits only inside a container; and a File or Directory whose location names a
// scheme this engine cannot read from.
var ErrUnsupportedFeature = errors.New("unsupported CWL feature")

// ErrToolExit reports a tool whose exit code [ClassifyExit] called a failure.
var ErrToolExit = errors.New("tool exited with a failure code")

// ErrInvocationDir reports an allocated output or scratch directory that is not an absolute path.
//
// Every File an invocation collects carries an absolute path derived from its output directory —
// [CollectOutputs] refuses a relative one outright — and resolving one here would silently tie the
// run's results to whichever directory this process happened to be started in.
var ErrInvocationDir = errors.New("an invocation directory must be an absolute path")

// Compile-time proof that the handler satisfies the contract.
var _ StepHandler = commandLineToolHandler{}

// commandLineToolHandler is the built-in handler for the CommandLineTool class. It runs the tool as
// an ordinary child process of this one, on this machine.
type commandLineToolHandler struct{}

// Execute runs one CommandLineTool invocation from end to end.
//
// The whole return path goes through [Outcome], not because the scheduler does not — it does — but
// because this handler has enough steps that "which of these returns is the invalid one" is a
// question worth never having to ask.
func (commandLineToolHandler) Execute(ctx context.Context, call *StepCall) (Result, error) {
	return Outcome(runCommandLineTool(ctx, call))
}

// runCommandLineTool prepares and runs one invocation.
func runCommandLineTool(ctx context.Context, call *StepCall) (Result, error) {
	run, err := newInvocation(call)
	if err != nil {
		return PermanentFail(err)
	}

	defer run.discardScratch()

	err = run.prepare()
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(call), err))
	}

	return run.execute(ctx)
}

// invocation is the resolved context of one CommandLineTool run: the directories it works in, the
// plan for filling them, and the input object every later stage reads.
type invocation struct {
	call    *StepCall
	tool    *cwlcore.CommandLineTool
	eval    *cwlcore.Evaluator
	mapper  *PathMap
	inputs  map[string]any
	runtime cwlcore.RuntimeContext
	outdir  string
	tmpdir  string
	scratch string
}

// newInvocation checks that the call is one this handler can run and allocates its directories.
func newInvocation(call *StepCall) (*invocation, error) {
	tool, ok := call.Process.(*cwlcore.CommandLineTool)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a CommandLineTool", ErrWrongProcessClass, describe(call))
	}

	err := checkSupported(call)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", describe(call), err)
	}

	run := &invocation{call: call, tool: tool, eval: call.Evaluator()}

	err = run.makeDirs()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", describe(call), err)
	}

	run.runtime = call.RuntimeContext()
	run.runtime.Outdir, run.runtime.Tmpdir = run.outdir, run.tmpdir

	return run, nil
}

// checkSupported refuses a call declaring a feature this engine does not have.
//
// Only a DockerRequirement in *requirements* is fatal. The distinction is the specification's own:
// a requirement must be honoured or the process must not run, whereas hints are advisory and "may
// be ignored". A workflow that hints at a container is describing a preference, and running its
// tool on this host is a legitimate reading of that hint — which is what an implementation without
// container support does. A requirement is not a preference, and running a container-targeted tool
// on the host would produce wrong results rather than missing ones.
func checkSupported(call *StepCall) error {
	if call.Requirements == nil {
		return nil
	}

	_, found, origin := call.Requirements.GetRequirement(cwlcore.ClassDockerRequirement)
	if !found {
		return nil
	}

	if origin == cwlcore.OriginHints {
		call.Log().Warn("running on the host: DockerRequirement is a hint and container execution is not implemented",
			"step", call.StepID)

		return nil
	}

	return fmt.Errorf("%w: DockerRequirement (container execution is not implemented)", ErrUnsupportedFeature)
}

// makeDirs creates the invocation's output and scratch directories.
//
// [StepCall.OutDir] and [StepCall.TmpDir] are paths, not directories: the scheduler derives them so
// they are stable across a resume and leaves creating them to whoever knows whether they are needed.
// A call that carries neither — a bare tool run through a zero Config — gets temporary directories
// instead, and the scratch one is removed when the invocation ends. The output directory is never
// removed: the outputs are in it.
func (i *invocation) makeDirs() error {
	outdir, err := ensureDir(i.call.OutDir, "cwl-out-")
	if err != nil {
		return err
	}

	i.outdir = outdir

	tmpdir, err := ensureDir(i.call.TmpDir, "cwl-tmp-")
	if err != nil {
		return err
	}

	i.tmpdir = tmpdir

	if i.call.TmpDir == "" {
		i.scratch = tmpdir
	}

	return nil
}

// ensureDir creates the directory at an allocated path, or a fresh temporary one when no path was
// allocated, and returns it. An allocated path must be absolute; see [ErrInvocationDir].
func ensureDir(path, prefix string) (string, error) {
	if path == "" {
		return os.MkdirTemp("", prefix)
	}

	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q", ErrInvocationDir, path)
	}

	return path, os.MkdirAll(path, stageDirPerm)
}

// discardScratch removes a temporary scratch directory this invocation created for itself. A
// scheduler-allocated one is left alone: it is addressed by a stable path a resumed invocation
// expects to find again, and its lifetime is the scheduler's to decide.
func (i *invocation) discardScratch() {
	if i.scratch == "" {
		return
	}

	err := os.RemoveAll(i.scratch)
	if err != nil {
		i.call.Log().Warn("could not remove the scratch directory", "dir", i.scratch, "err", err)
	}
}

// prepare fills the working directory and relocates the input object onto what it now contains.
//
// The order is forced. Literals are materialized first because a file literal has no path at all
// until something writes it; the InitialWorkDirRequirement is planned next, and its placements win,
// because a value it names must appear in the output directory under the name the document chose.
// Only then is the input object rewritten, and every stage after this point — argv, redirections,
// expressions, output collection — reads that rewritten object and nothing else.
func (i *invocation) prepare() error {
	i.mapper = NewPathMap(i.outdir, i.tmpdir)

	err := materializeLiterals(i.mapper, i.call.Inputs)
	if err != nil {
		return err
	}

	err = StageInitialWorkDir(i.mapper, i.call.Requirements, i.call.Inputs, i.eval, i.runtime)
	if err != nil {
		return err
	}

	err = i.mapper.Apply()
	if err != nil {
		return err
	}

	i.inputs = i.mapper.RewriteInputs(i.call.Inputs)

	return nil
}

// materializeLiterals plans a home for every File or Directory in an input object that has no path
// of its own, descending through records and arrays to reach them.
//
// Records are walked in sorted key order rather than in Go's map order, so that two runs of the same
// invocation give two literals sharing a basename the same two names.
func materializeLiterals(mapper *PathMap, value any) error {
	switch typed := value.(type) {
	case cwlcore.FileOrDirectory:
		return mapper.Materialize(typed)
	case map[string]any:
		return materializeRecord(mapper, typed)
	case []any:
		return materializeEach(mapper, typed)
	default:
		return nil
	}
}

// materializeRecord materializes every field of a record-valued parameter.
func materializeRecord(mapper *PathMap, object map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(object)) {
		err := materializeLiterals(mapper, object[key])
		if err != nil {
			return err
		}
	}

	return nil
}

// materializeEach materializes every element of an array-valued parameter.
func materializeEach(mapper *PathMap, values []any) error {
	for _, value := range values {
		err := materializeLiterals(mapper, value)
		if err != nil {
			return err
		}
	}

	return nil
}

// execute builds the command line, runs it, and turns what happened into a Result.
func (i *invocation) execute(ctx context.Context) (Result, error) {
	spec, err := i.spec()
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(i.call), err))
	}

	i.call.Log().Debug("running tool", "step", i.call.StepID, "argv", spec.argv(), "dir", spec.Dir)

	code, err := RunProcess(ctx, spec)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(i.call), err))
	}

	status := ClassifyExit(i.tool, code)
	if status != StatusSuccess {
		return Result{Status: status}, fmt.Errorf("%s: %w: %d", describe(i.call), ErrToolExit, code)
	}

	outputs, err := i.collect(code)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(i.call), err))
	}

	return Success(outputs)
}

// spec resolves everything the process needs: its argv, its environment, its redirections and its
// time limit.
func (i *invocation) spec() (*ProcessSpec, error) {
	line, err := BuildCommandLine(i.tool, i.inputs, i.eval, i.call.Requirements, i.runtime)
	if err != nil {
		return nil, err
	}

	env, err := ToolEnvironment(i.call.Requirements, i.inputs, i.eval, i.runtime)
	if err != nil {
		return nil, err
	}

	limit, err := ToolTimeLimit(i.call.Requirements, i.inputs, i.eval, i.runtime)
	if err != nil {
		return nil, err
	}

	spec := &ProcessSpec{Command: line, Dir: i.outdir, Env: env, Timeout: limit}

	return spec, i.redirect(spec)
}

// redirect resolves the three standard-stream redirections onto a spec.
func (i *invocation) redirect(spec *ProcessSpec) error {
	stdin, err := i.stdinPath()
	if err != nil {
		return err
	}

	stdout, err := i.capturePath(StreamStdout)
	if err != nil {
		return err
	}

	stderr, err := i.capturePath(StreamStderr)
	if err != nil {
		return err
	}

	spec.Stdin, spec.Stdout, spec.Stderr = stdin, stdout, stderr

	return nil
}

// stdinPath resolves the file connected to the tool's standard input, or "" when nothing is.
//
// A relative path resolves against the output directory, which is where the tool runs and where
// anything staged for it has been put.
func (i *invocation) stdinPath() (string, error) {
	if i.tool.Stdin == "" {
		return i.shortcutStdin(), nil
	}

	name, err := i.eval.EvalString(string(i.tool.Stdin), i.evalContext())
	if err != nil {
		return "", err
	}

	if name == "" {
		return "", nil
	}

	return outAbsolutize(name, i.outdir), nil
}

// shortcutStdin resolves the `stdin` type shortcut, which stands in for a tool-level redirection the
// document did not write.
//
// CommandLineTool.yml states the equivalence: an input of `type: stdin` is an input of `type: File`
// on a tool carrying `stdin: $(inputs.<id>.path)`. Reading the resolved input object rather than
// evaluating that expression reaches the same file and needs no evaluator.
func (i *invocation) shortcutStdin() string {
	for index := range i.tool.Inputs {
		param := &i.tool.Inputs[index]
		if param.Type.Kind() != cwlcore.TypeKindStdin {
			continue
		}

		file, ok := i.inputs[ShortName(param.ID())].(*cwlcore.File)
		if ok {
			return file.Path
		}
	}

	return ""
}

// capturePath returns the absolute file one standard stream is captured to, or "" when nothing keeps
// it.
//
// The filename comes from [StreamFile] rather than from anything decided here, because output
// collection globs for the same name and the two never speak to each other.
func (i *invocation) capturePath(stream Stream) (string, error) {
	if !i.captures(stream) {
		return "", nil
	}

	name, err := StreamFile(i.tool, stream, i.inputs, i.eval, i.runtime)
	if err != nil {
		return "", err
	}

	return filepath.Join(i.outdir, name), nil
}

// captures reports whether anything asks for a standard stream to be kept: the tool named a file for
// it, or an output parameter uses the matching type shortcut.
//
// A stream nobody keeps goes to the null device. Letting an unredirected tool write to this
// process's own standard output would corrupt the output object a cwl-runner prints there.
func (i *invocation) captures(stream Stream) bool {
	declared := i.tool.Stdout
	if stream == StreamStderr {
		declared = i.tool.Stderr
	}

	if declared != "" {
		return true
	}

	return slices.ContainsFunc(i.tool.Outputs, func(param cwlcore.CommandOutputParameter) bool {
		shortcut, ok := outShortcutStream(param.Type)

		return ok && shortcut == stream
	})
}

// collect produces the invocation's output object, from cwl.output.json when the tool wrote one and
// by output binding otherwise.
func (i *invocation) collect(exitCode int) (map[string]any, error) {
	outputs, err := LoadOutputJSON(i.tool, i.outdir, i.inputs)
	if err == nil {
		return outputs, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	return CollectOutputs(i.outputView(), i.outdir, exitCode, i.inputs, i.eval, i.runtime)
}

// evalContext builds the symbol environment this invocation's own expressions are evaluated against.
func (i *invocation) evalContext() *cwlcore.EvalContext {
	return &cwlcore.EvalContext{Inputs: outExpressionObject(i.inputs), Runtime: i.runtime}
}

// outputView returns the tool [CollectOutputs] should read: this invocation's tool, with the two
// things a requirement scope knows and a tool alone does not already resolved into it.
//
// Both are the same gap. CollectOutputs is handed a process, not a [cwlcore.RequirementScope], so
// anything an *enclosing workflow* declared is invisible to it — and for these two that is a
// wrong-answer failure rather than a loud one:
//
//   - A SchemaDefRequirement names the record and enum types an output may be declared as. One the
//     tool declares itself is resolved by CollectOutputs; one it inherits would leave the output's
//     type an unresolved name, and a record output with no fields collects nothing.
//   - Process.yml gives loadListing a three-step precedence — the binding's own setting, then a
//     LoadListingRequirement, then no_listing — of which CollectOutputs can see only the first.
//
// Resolving both into a per-invocation copy closes the gap without either side having to know about
// the other, and never mutates the decoded document, which a scattered step's concurrent sub-jobs
// share.
func (i *invocation) outputView() *cwlcore.CommandLineTool {
	mode, _ := loadListingDefault(i.call.Requirements)

	view := *i.tool
	view.Outputs = slices.Clone(i.tool.Outputs)

	for index := range view.Outputs {
		param := &view.Outputs[index]

		// Unconditional on purpose: ResolveTypeRef descends through arrays, unions and nested
		// record fields in one call, leaves anything unresolvable alone, and is a no-op when
		// no SchemaDefRequirement is in scope.
		param.Type = cwlcore.ResolveTypeRef(i.call.Requirements, param.Type)

		if mode == "" {
			continue
		}

		param.OutputBinding = relistBinding(param.OutputBinding, mode)
		param.Type = relistType(param.Type, mode)
	}

	return &view
}

// relistBinding returns binding with mode filled in, unless it set `loadListing` itself — in which
// case the binding wins, which is the first step of the precedence — or there is no binding to fill
// in at all.
func relistBinding(binding *cwlcore.CommandOutputBinding, mode cwlcore.LoadListingEnum) *cwlcore.CommandOutputBinding {
	if binding == nil || binding.LoadListing != "" {
		return binding
	}

	relisted := *binding
	relisted.LoadListing = mode

	return &relisted
}

// relistType fills mode into every output binding reachable inside a declared type, descending
// through arrays, unions and record fields.
//
// A record field carries an outputBinding of its own, so a Directory-typed field is subject to the
// same three-step precedence as a top-level output and must inherit the requirement the same way.
// The walk terminates because [cwlcore.ResolveTypeRef] refuses to expand a type into itself, so the
// resolved graph is finite.
func relistType(declared cwlcore.TypeRef, mode cwlcore.LoadListingEnum) cwlcore.TypeRef {
	switch declared.Kind() {
	case cwlcore.TypeKindRecord:
		return relistRecord(declared, mode)
	case cwlcore.TypeKindArray:
		return relistArray(declared, mode)
	case cwlcore.TypeKindUnion:
		return cwlcore.NewUnionType(relistOptions(declared.Options(), mode)).WithNode(declared.Node())
	default:
		return declared
	}
}

// relistRecord fills mode into each of a record's field bindings, and into the types those fields
// are themselves declared as.
func relistRecord(declared cwlcore.TypeRef, mode cwlcore.LoadListingEnum) cwlcore.TypeRef {
	schema := declared.Record()
	if schema == nil {
		return declared
	}

	relisted := *schema
	relisted.Fields = slices.Clone(schema.Fields)

	for index := range relisted.Fields {
		field := &relisted.Fields[index]
		field.OutputBinding = relistBinding(field.OutputBinding, mode)
		field.Type = relistType(field.Type, mode)
	}

	return cwlcore.NewRecordType(&relisted).WithNode(declared.Node())
}

// relistArray fills mode into the type an array's elements are declared as.
func relistArray(declared cwlcore.TypeRef, mode cwlcore.LoadListingEnum) cwlcore.TypeRef {
	schema := declared.Array()
	if schema == nil {
		return declared
	}

	relisted := *schema
	relisted.Items = relistType(schema.Items, mode)

	return cwlcore.NewArrayType(&relisted).WithNode(declared.Node())
}

// relistOptions fills mode into every member of a union.
func relistOptions(options []cwlcore.TypeRef, mode cwlcore.LoadListingEnum) []cwlcore.TypeRef {
	relisted := make([]cwlcore.TypeRef, 0, len(options))
	for _, option := range options {
		relisted = append(relisted, relistType(option, mode))
	}

	return relisted
}

// loadListingDefault resolves the LoadListingRequirement in effect for a scope.
func loadListingDefault(scope *cwlcore.RequirementScope) (cwlcore.LoadListingEnum, bool) {
	if scope == nil {
		return "", false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassLoadListingRequirement)
	if !found {
		return "", false
	}

	typed, ok := requirement.(*cwlcore.LoadListingRequirement)
	if !ok {
		return "", false
	}

	return typed.LoadListing, true
}
