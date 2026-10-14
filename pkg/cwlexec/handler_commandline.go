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

// ErrUnsupportedFeature reports a recognized but unimplemented CWL feature.
// More specific sentinels wrap it: [ErrUnsupportedLocationScheme] and [ErrUnsupportedImageSource].
var ErrUnsupportedFeature = errors.New("unsupported CWL feature")

// ErrUnsupportedLocationScheme reports a File or Directory whose location names a URI scheme
// this engine cannot read from (e.g. s3://, http://). Wraps [ErrUnsupportedFeature].
var ErrUnsupportedLocationScheme = fmt.Errorf("%w: unsupported location scheme", ErrUnsupportedFeature)

// ErrUnsupportedImageSource reports a container image acquisition method this executor has not
// implemented (remote dockerLoad, dockerImport, Dockerfile builds). Wraps [ErrUnsupportedFeature].
var ErrUnsupportedImageSource = fmt.Errorf("%w: unsupported image source", ErrUnsupportedFeature)

// ErrToolExit reports a tool that exited with a failure code.
var ErrToolExit = errors.New("tool exited with a failure code")

// ErrInvocationDir reports a non-absolute output or scratch directory.
var ErrInvocationDir = errors.New("an invocation directory must be an absolute path")

var _ StepHandler = commandLineToolHandler{}

// commandLineToolHandler is the built-in CommandLineTool handler.
type commandLineToolHandler struct{}

// Execute runs one CommandLineTool invocation.
func (commandLineToolHandler) Execute(ctx context.Context, call *StepCall) (Result, error) {
	return Outcome(runCommandLineTool(ctx, call))
}

// runCommandLineTool prepares and runs one invocation, returning its Result.
func runCommandLineTool(ctx context.Context, call *StepCall) (Result, error) {
	run, err := newInvocation(call)
	if err != nil {
		return PermanentFail(err)
	}

	result, runErr := runAndClose(ctx, call, run)

	return result, runErr
}

func runAndClose(ctx context.Context, call *StepCall, run *invocation) (Result, error) {
	err := run.prepare(ctx)
	if err != nil {
		closeErr := run.closeInvocation()

		result, prepErr := PermanentFail(fmt.Errorf("%s: %w", describe(call), err))

		return result, errors.Join(prepErr, closeErr)
	}

	result, execErr := run.execute(ctx)
	closeErr := run.closeInvocation()

	return result, errors.Join(execErr, closeErr)
}

// invocation is the resolved context of one CommandLineTool run.
type invocation struct {
	call     *StepCall
	tool     *cwlcore.CommandLineTool
	eval     *cwlcore.Evaluator
	mapper   *PathMap
	inputs   map[string]any
	docker   *cwlcore.DockerRequirement
	box      *container
	executor ContainerExecutor
	inv      Invocation
	runtime  cwlcore.RuntimeContext
	outdir   string
	tmpdir   string

	// absolute allows listing entries to target paths outside the working directory.
	absolute bool
}

// newInvocation validates the call and allocates directories.
func newInvocation(call *StepCall) (*invocation, error) {
	tool, ok := call.Process.(*cwlcore.CommandLineTool)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a CommandLineTool", ErrWrongProcessClass, describe(call))
	}

	docker, absolute, err := resolveDocker(call)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", describe(call), err)
	}

	if docker != nil {
		return newContainedInvocation(call, tool, docker, absolute)
	}

	return newHostInvocation(call, tool)
}

// newHostInvocation constructs a non-container invocation.
func newHostInvocation(call *StepCall, tool *cwlcore.CommandLineTool) (*invocation, error) {
	local, err := newLocalInvocation(call.OutDir, call.TmpDir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", describe(call), err)
	}

	rt := call.RuntimeContext()
	rt.Outdir = local.OutDir()
	rt.Tmpdir = local.TmpDir()

	return &invocation{
		call:     call,
		tool:     tool,
		eval:     call.Evaluator(),
		mapper:   nil,
		inputs:   nil,
		docker:   nil,
		box:      nil,
		executor: call.ContainerExecutor,
		inv:      local,
		runtime:  rt,
		outdir:   local.OutDir(),
		tmpdir:   local.TmpDir(),
		absolute: false,
	}, nil
}

// newContainedInvocation constructs a container invocation. The [Invocation] is created
// later in [invocation.createContainerInvocation].
func newContainedInvocation(
	call *StepCall, tool *cwlcore.CommandLineTool,
	docker *cwlcore.DockerRequirement, absolute bool,
) (*invocation, error) {
	outdir, err := ensureDir(call.OutDir, "cwl-out-")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", describe(call), err)
	}

	tmpdir, err := ensureDir(call.TmpDir, "cwl-tmp-")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", describe(call), err)
	}

	box := newContainer(docker, outdir, tmpdir, call.Containers)

	err = box.dirs()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", describe(call), err)
	}

	rt := call.RuntimeContext()
	rt.Outdir = box.toolOutdir
	rt.Tmpdir = containerTmpdir

	return &invocation{
		call:     call,
		tool:     tool,
		eval:     call.Evaluator(),
		mapper:   nil,
		inputs:   nil,
		docker:   docker,
		box:      box,
		executor: call.ContainerExecutor,
		inv:      nil,
		runtime:  rt,
		outdir:   outdir,
		tmpdir:   tmpdir,
		absolute: absolute,
	}, nil
}

// resolveDocker returns the DockerRequirement and whether absolute entrynames are allowed.
// Nil means no container.
func resolveDocker(
	call *StepCall,
) (*cwlcore.DockerRequirement, bool, error) {
	declared, origin, found := dockerRequirement(call.Requirements)
	if !found {
		return nil, false, nil
	}

	if call.Containers.Disabled || call.ContainerExecutor == nil {
		return nil, false, declineContainer(call, origin)
	}

	call.Log().Debug("running in a container", "step", call.StepID,
		"image", imageReference(declared), "origin", origin)

	return declared, origin == cwlcore.OriginRequirements, nil
}

// declineContainer handles a DockerRequirement when containers are disabled.
// Hints are silently skipped; requirements return [ErrUnsupportedFeature].
func declineContainer(call *StepCall, origin cwlcore.RequirementOrigin) error {
	if origin == cwlcore.OriginRequirements {
		return fmt.Errorf("%w: containers are disabled, but %s is declared under requirements",
			ErrUnsupportedFeature, cwlcore.ClassDockerRequirement)
	}

	call.Log().Debug("containers are disabled; declining the DockerRequirement hint",
		"step", call.StepID, "origin", origin)

	return nil
}

// dockerRequirement returns the DockerRequirement in scope and its origin.
func dockerRequirement(
	scope *cwlcore.RequirementScope,
) (*cwlcore.DockerRequirement, cwlcore.RequirementOrigin, bool) {
	if scope == nil {
		return nil, cwlcore.OriginNone, false
	}

	requirement, found, origin := scope.GetRequirement(cwlcore.ClassDockerRequirement)
	if !found {
		return nil, cwlcore.OriginNone, false
	}

	typed, ok := requirement.(*cwlcore.DockerRequirement)

	return typed, origin, ok
}

// prepare materializes literals, stages the working directory, and rewrites the input object.
func (i *invocation) prepare(ctx context.Context) error {
	err := i.acquireImage(ctx)
	if err != nil {
		return err
	}

	i.mapper = i.newMapper()

	err = materializeLiterals(i.mapper, i.call.Inputs)
	if err != nil {
		return err
	}

	err = StageInitialWorkDir(i.mapper, i.call.Requirements, i.call.Inputs, i.eval, i.runtime)
	if err != nil {
		return err
	}

	err = i.createContainerInvocation(ctx)
	if err != nil {
		return err
	}

	err = i.mapper.Apply(i.inv.OutFS(), i.inv.StageFS())
	if err != nil {
		return err
	}

	i.inputs = i.mapper.RewriteInputs(i.call.Inputs)

	return nil
}

// createContainerInvocation creates the [Invocation] for container tools. No-op if non-container.
func (i *invocation) createContainerInvocation(ctx context.Context) error {
	if i.docker == nil {
		return nil
	}

	network, err := ToolNetworkAccess(i.call.Requirements, i.call.Inputs, i.eval, i.runtime)
	if err != nil {
		return err
	}

	ctr := i.box.containerSpec(i.mapper.Plan(), network)

	inv, err := i.executor.NewInvocation(ctx, ctr)
	if err != nil {
		return err
	}

	i.inv = inv

	return nil
}

// acquireImage ensures the container image is available. No-op for host tools.
func (i *invocation) acquireImage(ctx context.Context) error {
	if i.docker == nil || i.executor == nil {
		return nil
	}

	return i.executor.EnsureImage(ctx, i.docker)
}

// newMapper builds a host or container path map for this invocation.
func (i *invocation) newMapper() *PathMap {
	if i.box == nil {
		m := NewPathMap(i.outdir, i.tmpdir)
		m.resolver = i.call.OutputResolver

		return m
	}

	mapper := i.box.mapper()
	mapper.resolver = i.call.OutputResolver

	if i.absolute {
		mapper.AllowAbsoluteTargets()
	}

	return mapper
}

// materializeLiterals assigns paths to File/Directory values that have none.
// Records are walked in sorted key order for determinism.
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

// materializeRecord materializes all fields of a record value.
func materializeRecord(mapper *PathMap, object map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(object)) {
		err := materializeLiterals(mapper, object[key])
		if err != nil {
			return err
		}
	}

	return nil
}

// materializeEach materializes all elements of an array value.
func materializeEach(mapper *PathMap, values []any) error {
	for _, value := range values {
		err := materializeLiterals(mapper, value)
		if err != nil {
			return err
		}
	}

	return nil
}

// execute builds the command line, runs the tool, and collects outputs.
func (i *invocation) execute(ctx context.Context) (Result, error) {
	spec, err := i.spec()
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(i.call), err))
	}

	i.call.Log().Debug("running tool", "step", i.call.StepID, "argv", spec.argv(), "dir", spec.Dir)

	code, err := i.inv.Run(ctx, spec)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(i.call), err))
	}

	// Restore symlinks that were bind-mounted during container execution.
	err = i.mapper.Relink(i.inv.OutFS(), i.inv.StageFS())
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(i.call), err))
	}

	status := ClassifyExit(i.tool, code)
	if status != StatusSuccess {
		return Result{
				Status:     status,
				Outputs:    nil,
				Suspension: nil,
			}, fmt.Errorf(
				"%s: %w: %d",
				describe(i.call),
				ErrToolExit,
				code,
			)
	}

	outputs, err := i.collect(code)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(i.call), err))
	}

	if rw, ok := i.inv.(OutputRewriter); ok {
		outputs = rw.RewriteOutputPaths(outputs)
	}

	return Success(outputs)
}

// spec resolves the tool's argv, environment, redirections, and time limit.
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

	if i.box != nil {
		env = withoutInheritedPath(env, i.call.Requirements)
	}

	spec := &ProcessSpec{Command: line, Dir: i.outdir, Stdin: "", Stdout: "", Stderr: "", Env: env, Timeout: limit}

	err = i.redirect(spec)
	if err != nil {
		return nil, err
	}

	return spec, nil
}

// closeInvocation releases invocation resources and removes auto-allocated tmpdirs.
func (i *invocation) closeInvocation() error {
	if i.inv == nil {
		return nil
	}

	closeErr := i.inv.Close()

	if i.box != nil && i.call.TmpDir == "" && i.tmpdir != "" {
		closeErr = errors.Join(closeErr, os.RemoveAll(i.tmpdir))
	}

	return closeErr
}

// redirect resolves stdin/stdout/stderr redirections.
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

// stdinPath resolves the file for the tool's stdin, or "" if none.
func (i *invocation) stdinPath() (string, error) {
	seen, err := i.declaredStdin()
	if err != nil || seen == "" {
		return "", err
	}

	return i.mapper.hostSource(outAbsolutize(seen, i.runtime.Outdir)), nil
}

// declaredStdin evaluates the tool's stdin expression.
func (i *invocation) declaredStdin() (string, error) {
	if i.tool.Stdin == "" {
		return i.shortcutStdin(), nil
	}

	return i.eval.EvalString(string(i.tool.Stdin), i.evalContext())
}

// shortcutStdin resolves the `stdin` type shortcut by finding the File input.
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

// capturePath returns the absolute file a stream is captured to, or "" if uncaptured.
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

// captures reports whether the stream is captured by a declaration or type shortcut.
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

// collect produces the output object from cwl.output.json or output bindings.
func (i *invocation) collect(exitCode int) (map[string]any, error) {
	view, inputs := i.outputView(), i.hostInputs()

	outfs := i.inv.OutFS()

	outputs, err := LoadOutputJSON(view, i.outdir, outfs, inputs, WithHostPaths(i.mapper.hostOutputPath))
	if err == nil {
		return outputs, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	return CollectOutputs(view, i.outdir, outfs, exitCode, inputs, i.eval, i.runtime)
}

// hostInputs returns the input object with host-side paths for output collection.
func (i *invocation) hostInputs() map[string]any {
	if i.box == nil {
		return i.inputs
	}

	return i.mapper.hostView().RewriteInputs(i.inputs)
}

// evalContext builds the expression evaluation context for this invocation.
func (i *invocation) evalContext() *cwlcore.EvalContext {
	return &cwlcore.EvalContext{Inputs: outExpressionObject(i.inputs), Self: nil, Runtime: i.runtime}
}

// outputView returns a copy of the tool with inherited requirements (SchemaDefRequirement,
// LoadListingRequirement, InitialWorkDirRequirement) resolved for output collection.
func (i *invocation) outputView() *cwlcore.CommandLineTool {
	mode, _ := loadListingDefault(i.call.Requirements)

	view := *i.tool
	view.Outputs = slices.Clone(i.tool.Outputs)
	view.Requirements = i.stagedRequirements()

	for index := range view.Outputs {
		param := &view.Outputs[index]

		param.Type = cwlcore.ResolveTypeRef(i.call.Requirements, param.Type)

		if mode == "" {
			continue
		}

		param.OutputBinding = relistBinding(param.OutputBinding, mode)
		param.Type = relistType(param.Type, mode)
	}

	return &view
}

// stagedRequirements appends any inherited InitialWorkDirRequirement to the tool's own.
func (i *invocation) stagedRequirements() []cwlcore.ProcessRequirement {
	inherited, found := initialWorkDir(i.call.Requirements)
	if !found {
		return i.tool.Requirements
	}

	_, own := initialWorkDir(cwlcore.NewScope(i.tool))
	if own {
		return i.tool.Requirements
	}

	return append(slices.Clone(i.tool.Requirements), inherited)
}
