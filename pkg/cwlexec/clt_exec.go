package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Running the tool.
//
// Everything above this file decides *what* to run — the argv, the working directory, the files in
// it, the environment. This file spawns it, waits for it, and reports the exit code. It reads no CWL
// document beyond the three requirements that describe the process itself, and it knows nothing
// about output collection.

// Errors reported while running a tool's process. They are wrapped with context, so callers should
// test them with [errors.Is].
var (
	// ErrEmptyCommand reports a CommandLineTool that produced no argv at all: no baseCommand, no
	// arguments and no bound inputs. There is nothing to execute, and executing the first
	// argument of an empty list is how an engine runs the wrong program.
	ErrEmptyCommand = errors.New("command line is empty")

	// ErrToolTimeLimit reports a tool killed for exceeding its ToolTimeLimit. The specification
	// requires the implementation to "terminate the process" and treat the outcome as a
	// permanent failure, so this is never a temporary one.
	ErrToolTimeLimit = errors.New("tool exceeded its ToolTimeLimit")

	// ErrTimeLimitValue reports a ToolTimeLimit whose `timelimit` expression did not evaluate to
	// a non-negative number of seconds.
	ErrTimeLimitValue = errors.New("ToolTimeLimit timelimit is not a non-negative number of seconds")
)

// The environment variables the specification names for a tool's process. See [ToolEnvironment].
const (
	envHome   = "HOME"
	envTmpDir = "TMPDIR"
	envPath   = "PATH"
)

// shellPath is the shell a ShellCommandRequirement's command string is handed to. The specification
// describes the result as "a single string containing a shell command line", and POSIX puts the
// shell that interprets one at this path.
const shellPath = "/bin/sh"

// ProcessSpec is one operating-system process to run on behalf of a CommandLineTool: everything
// os/exec needs, and nothing about the document it came from.
//
// It is the boundary a container implementation replaces. Every field here is already resolved —
// the argv is built, the paths are the ones the tool will see, the environment is complete — so an
// alternative executor consumes the same value and differs only in how it starts the process.
type ProcessSpec struct {
	// Command is the argv, and whether it is to be run through a shell; see [CommandLine].
	Command *CommandLine

	// Dir is the working directory the process starts in, which is the tool's designated output
	// directory.
	Dir string

	// Stdin is the file to connect to the process's standard input, or "" to give it none.
	Stdin string

	// Stdout is the file the process's standard output is captured to, or "" to discard it.
	//
	// Discarding is deliberate. A cwl-runner writes its output object to its own standard
	// output, so a tool's output must never reach it by default; an undeclared stream that some
	// output parameter nevertheless captures is given a filename by [StreamFile], and the caller
	// puts that name here.
	Stdout string

	// Stderr is the file the process's standard error is captured to, or "" to discard it.
	Stderr string

	// Env is the process's complete environment, as KEY=VALUE strings. An empty slice means an
	// empty environment, not an inherited one; see [ToolEnvironment].
	Env []string

	// Timeout bounds the process's wall-clock run time. Zero means no limit.
	Timeout time.Duration
}

// RunProcess spawns the process spec describes, waits for it, and returns its exit code.
//
// A non-zero exit is not an error: deciding what an exit code means is [ClassifyExit]'s job, and a
// tool that reports a meaningful failure code is still a tool that ran. An error is returned only
// when the process could not be run, could not be waited for, or was killed — by ctx, or by its own
// ToolTimeLimit, which is reported as [ErrToolTimeLimit].
//
// ctx cancellation kills the process rather than orphaning it, so a cancelled run leaves nothing
// behind still writing to the output directory.
func RunProcess(ctx context.Context, spec *ProcessSpec) (int, error) {
	argv := spec.argv()
	if len(argv) == 0 {
		return 0, ErrEmptyCommand
	}

	streams, err := openStreams(spec)
	if err != nil {
		return 0, err
	}

	code, runErr := runArgv(ctx, spec, argv, streams)

	return code, errors.Join(runErr, streams.close())
}

// argv renders the command line as the argument vector to spawn.
//
// The two forms are genuinely different invocations. Without a ShellCommandRequirement the argv is
// handed to the operating system as it stands and no shell ever sees it, so a metacharacter in an
// argument is an ordinary character. With one, the specification requires the elements to be "joined
// into a string separated by single spaces and quoted to prevent interpretation by the shell, unless
// CommandLineBinding for that argument contains shellQuote: false", and that string is what /bin/sh
// is asked to interpret.
func (s *ProcessSpec) argv() []string {
	if s.Command == nil {
		return nil
	}

	if s.Command.Shell {
		return []string{shellPath, "-c", s.Command.ShellCommand()}
	}

	return s.Command.Argv()
}

// runArgv spawns and waits for one already-opened process.
func runArgv(ctx context.Context, spec *ProcessSpec, argv []string, streams *processStreams) (int, error) {
	runCtx, cancel := spec.deadline(ctx)
	defer cancel()

	program, arguments := argv[0], argv[1:]

	cmd := exec.CommandContext(runCtx, program, arguments...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	streams.attach(cmd)

	runErr := cmd.Run()
	if runErr == nil {
		return 0, nil
	}

	killed := killedBy(ctx, runCtx, spec.Timeout)
	if killed != nil {
		return 0, killed
	}

	var exited *exec.ExitError
	if !errors.As(runErr, &exited) {
		return 0, fmt.Errorf("running %q: %w", program, runErr)
	}

	return exited.ExitCode(), nil
}

// deadline derives the context the process actually runs under, applying the tool's time limit when
// it has one.
func (s *ProcessSpec) deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.Timeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, s.Timeout)
}

// killedBy reports why a process was killed, or nil when it ended on its own terms.
//
// The caller's own cancellation is reported first and unchanged, because a caller that stopped the
// run wants its own error back rather than a diagnosis of the symptom.
func killedBy(ctx, runCtx context.Context, timeout time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w of %s", ErrToolTimeLimit, timeout)
	}

	return nil
}

// processStreams holds the files a process's three standard streams are wired to. A nil member means
// the stream is not redirected to a file.
type processStreams struct {
	in  *os.File
	out *os.File
	err *os.File
}

// openStreams opens or creates the files a spec's redirections name.
func openStreams(spec *ProcessSpec) (*processStreams, error) {
	streams := &processStreams{in: nil, out: nil, err: nil}

	if spec.Stdin != "" {
		file, err := os.Open(filepath.Clean(spec.Stdin))
		if err != nil {
			return nil, fmt.Errorf("opening stdin: %w", err)
		}

		streams.in = file
	}

	err := streams.capture(spec.Stdout, spec.Stderr)
	if err != nil {
		return nil, errors.Join(err, streams.close())
	}

	return streams, nil
}

// capture creates the files the two output streams are captured to. An empty path captures nothing,
// which leaves that stream connected to the null device.
func (s *processStreams) capture(stdout, stderr string) error {
	if stdout != "" {
		file, err := createStream(stdout)
		if err != nil {
			return fmt.Errorf("creating stdout: %w", err)
		}

		s.out = file
	}

	if stderr == "" {
		return nil
	}

	file, err := createStream(stderr)
	if err != nil {
		return fmt.Errorf("creating stderr: %w", err)
	}

	s.err = file

	return nil
}

// createStream creates the file a captured stream is written to, making its parent directory when
// the tool's redirection names a subdirectory.
func createStream(path string) (*os.File, error) {
	err := os.MkdirAll(filepath.Dir(path), stageDirPerm)
	if err != nil {
		return nil, err
	}

	return os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, stageFilePerm)
}

// attach wires the opened files onto a command, leaving an unredirected stream connected to the
// null device, which is what os/exec does with a nil field.
//
// The nil checks are load-bearing rather than defensive. Assigning an unset file to cmd.Stdout would
// store a non-nil [io.Writer] holding a nil pointer, which os/exec would then dutifully write to;
// leaving the field alone is what gets /dev/null.
func (s *processStreams) attach(cmd *exec.Cmd) {
	if s.in != nil {
		cmd.Stdin = s.in
	}

	if s.out != nil {
		cmd.Stdout = s.out
	}

	if s.err != nil {
		cmd.Stderr = s.err
	}
}

// close closes every opened file, reporting all failures rather than the first.
func (s *processStreams) close() error {
	var err error

	for _, file := range []*os.File{s.in, s.out, s.err} {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
	}

	return err
}

// ToolEnvironment builds the complete environment a tool's process runs in.
//
// The specification is unusually direct about this, and the rule is *not* inheritance:
//
//	When executing the tool, the tool must execute in a new, empty environment with only the
//	environment variables described below; the child process must not inherit environment
//	variables from the parent process except as specified or at user option.
//
// The variables it then describes are HOME, set to the designated output directory; TMPDIR, set to
// the designated temporary directory; PATH, which "may be inherited from the parent process"; and
// whatever an EnvVarRequirement declares. So the environment here is built from nothing: three
// variables plus the document's own, and not one thing more. A tool that reads any other variable
// gets the same answer on every machine, which is the point.
//
// An EnvVarRequirement value may be an expression and is evaluated against inputs and rt. A
// declaration may deliberately override HOME, TMPDIR or PATH, so the requirement's variables are
// applied last.
func ToolEnvironment(scope *cwlcore.RequirementScope, inputs map[string]any,
	eval *cwlcore.Evaluator, rt cwlcore.RuntimeContext,
) ([]string, error) {
	env := map[string]string{
		envHome:   rt.Outdir,
		envTmpDir: rt.Tmpdir,
		envPath:   os.Getenv(envPath),
	}

	err := applyEnvVars(env, scope, inputs, eval, rt)
	if err != nil {
		return nil, err
	}

	rendered := make([]string, 0, len(env))
	for name, value := range env {
		rendered = append(rendered, name+"="+value)
	}

	// Sorted, so that two runs of the same tool are handed byte-identical environments and a
	// test can assert one without depending on Go's map iteration order.
	slices.Sort(rendered)

	return rendered, nil
}

// applyEnvVars adds the variables an EnvVarRequirement in scope declares, evaluating each value.
func applyEnvVars(env map[string]string, scope *cwlcore.RequirementScope, inputs map[string]any,
	eval *cwlcore.Evaluator, rt cwlcore.RuntimeContext,
) error {
	declared, found := envVarRequirement(scope)
	if !found {
		return nil
	}

	symbols := &cwlcore.EvalContext{Inputs: outExpressionObject(inputs), Self: nil, Runtime: rt}

	for index := range declared.EnvDef {
		definition := &declared.EnvDef[index]

		value, err := eval.EvalString(string(definition.EnvValue), symbols)
		if err != nil {
			return fmt.Errorf("environment variable %q: %w", definition.EnvName, err)
		}

		env[definition.EnvName] = value
	}

	return nil
}

// envVarDeclares reports whether an EnvVarRequirement in scope sets a variable itself, as opposed
// to [ToolEnvironment] having filled it in.
func envVarDeclares(scope *cwlcore.RequirementScope, name string) bool {
	declared, found := envVarRequirement(scope)
	if !found {
		return false
	}

	return slices.ContainsFunc(declared.EnvDef, func(definition cwlcore.EnvironmentDef) bool {
		return definition.EnvName == name
	})
}

// withoutInheritedPath drops the PATH this process inherited, for a tool that is about to run
// somewhere this process's PATH does not describe.
//
// [ToolEnvironment] sets PATH from this process's own, which the specification permits: "PATH...
// may be inherited from the parent process". Inside a container the environment to inherit from is
// the image's, not this machine's — cwltool's DockerCommandLineJob._required_env passes TMPDIR and
// HOME and nothing else — and forcing a host PATH onto a container hides the image's own, so a tool
// whose program lives somewhere this host does not have it is not found. HOME and TMPDIR are
// unaffected: both were resolved to the paths the tool sees before they were rendered.
//
// A PATH an EnvVarRequirement declared is the document's rather than this process's, and stays.
func withoutInheritedPath(env []string, scope *cwlcore.RequirementScope) []string {
	if envVarDeclares(scope, envPath) {
		return env
	}

	prefix := envPath + "="

	kept := make([]string, 0, len(env))
	for _, variable := range env {
		if !strings.HasPrefix(variable, prefix) {
			kept = append(kept, variable)
		}
	}

	return kept
}

// ToolNetworkAccess reports whether the tool may reach the network outside its own machine.
//
// CommandLineTool.yml, NetworkAccess: "Indicate whether a process requires outgoing IPv4/IPv6
// network access. Choice of IPv4 or IPv6 is implementation and site specific... If `networkAccess`
// is false or not specified, tools must not assume network access, except for localhost." So the
// default is off, and the field may be written as an expression over the invocation's inputs.
//
// It is a question only an executor that can *withhold* the network has any use for, which on this
// host is none: a child process has whatever network this one has. A container is where the answer
// becomes actionable, and conformance test networkaccess_disabled — a should_fail test whose tool
// declares nothing and tries to open a connection — is what asserts the default is honoured.
func ToolNetworkAccess(scope *cwlcore.RequirementScope, inputs map[string]any,
	eval *cwlcore.Evaluator, rt cwlcore.RuntimeContext,
) (bool, error) {
	declared, found := networkAccess(scope)
	if !found || !declared.NetworkAccess.IsSet() {
		return false, nil
	}

	if declared.NetworkAccess.Kind() != cwlcore.ValueExpression {
		return declared.NetworkAccess.Bool(), nil
	}

	return eval.EvalBool(string(declared.NetworkAccess.Expression()),
		&cwlcore.EvalContext{Inputs: outExpressionObject(inputs), Self: nil, Runtime: rt})
}

// networkAccess resolves the NetworkAccess requirement in effect for a scope. A declaration in
// hints counts: the field is a statement about what the tool needs, and an engine that can grant it
// has no reason to withhold it because the author wrote the statement advisorily.
func networkAccess(scope *cwlcore.RequirementScope) (*cwlcore.NetworkAccess, bool) {
	if scope == nil {
		return nil, false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassNetworkAccess)
	if !found {
		return nil, false
	}

	typed, ok := requirement.(*cwlcore.NetworkAccess)

	return typed, ok
}

// envVarRequirement resolves the EnvVarRequirement in effect for a scope.
func envVarRequirement(scope *cwlcore.RequirementScope) (*cwlcore.EnvVarRequirement, bool) {
	if scope == nil {
		return nil, false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassEnvVarRequirement)
	if !found {
		return nil, false
	}

	typed, ok := requirement.(*cwlcore.EnvVarRequirement)

	return typed, ok
}

// ToolTimeLimit returns how long a tool may run before it must be killed, or zero when nothing
// bounds it.
//
// CommandLineTool.yml, ToolTimeLimit.timelimit: the limit is "the time in seconds", and "if the
// value is zero or an expression evaluating to zero, this means no time limit is set". A negative
// limit is not a shorter one; it is a document that does not mean anything, so it is refused.
func ToolTimeLimit(scope *cwlcore.RequirementScope, inputs map[string]any,
	eval *cwlcore.Evaluator, rt cwlcore.RuntimeContext,
) (time.Duration, error) {
	declared, found := toolTimeLimit(scope)
	if !found {
		return 0, nil
	}

	seconds, err := timeLimitSeconds(declared.Timelimit, inputs, eval, rt)
	if err != nil {
		return 0, err
	}

	if seconds < 0 {
		return 0, fmt.Errorf("%w: %d", ErrTimeLimitValue, seconds)
	}

	return time.Duration(seconds) * time.Second, nil
}

// timeLimitSeconds resolves a timelimit that may be written as a literal or as an expression.
func timeLimitSeconds(declared cwlcore.ExprLong, inputs map[string]any,
	eval *cwlcore.Evaluator, rt cwlcore.RuntimeContext,
) (int64, error) {
	if declared.Kind() != cwlcore.ValueExpression {
		return declared.Int(), nil
	}

	value, err := eval.Eval(string(declared.Expression()),
		&cwlcore.EvalContext{Inputs: outExpressionObject(inputs), Self: nil, Runtime: rt})
	if err != nil {
		return 0, err
	}

	seconds, ok := outNumber(value)
	if !ok {
		return 0, fmt.Errorf("%w: got %s", ErrTimeLimitValue, cwlcore.TypeName(value))
	}

	return seconds, nil
}

// toolTimeLimit resolves the ToolTimeLimit in effect for a scope.
func toolTimeLimit(scope *cwlcore.RequirementScope) (*cwlcore.ToolTimeLimit, bool) {
	if scope == nil {
		return nil, false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassToolTimeLimit)
	if !found {
		return nil, false
	}

	typed, ok := requirement.(*cwlcore.ToolTimeLimit)

	return typed, ok
}
