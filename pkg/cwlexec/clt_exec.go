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

// Spawning and waiting for tool processes.

// Errors reported while running a tool's process. Use [errors.Is] to test.
var (
	// ErrEmptyCommand reports an empty argv.
	ErrEmptyCommand = errors.New("command line is empty")
	// ErrToolTimeLimit reports a tool killed for exceeding its time limit.
	ErrToolTimeLimit = errors.New("tool exceeded its ToolTimeLimit")
	// ErrTimeLimitValue reports a timelimit that is not a non-negative number.
	ErrTimeLimitValue = errors.New("ToolTimeLimit timelimit is not a non-negative number of seconds")
)

// Spec-defined environment variables for a tool's process.
const (
	envHome   = "HOME"
	envTmpDir = "TMPDIR"
	envPath   = "PATH"
)

// shellPath is the POSIX shell used for ShellCommandRequirement.
const shellPath = "/bin/sh"

// ProcessSpec is the resolved process to run for a CommandLineTool invocation.
type ProcessSpec struct {
	Command *CommandLine  // argv and shell flag
	Dir     string        // working directory (output directory)
	Stdin   string        // file for stdin, or "" for none
	Stdout  string        // file for stdout, or "" to discard
	Stderr  string        // file for stderr, or "" to discard
	Env     []string      // complete environment as KEY=VALUE strings
	Timeout time.Duration // wall-clock limit; zero means unlimited
}

// RunProcess spawns the process, waits for it, and returns its exit code.
// A non-zero exit is not an error; only spawn/wait/kill failures are.
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
func (s *ProcessSpec) argv() []string {
	if s.Command == nil {
		return nil
	}

	if s.Command.Shell {
		return []string{shellPath, "-c", s.Command.ShellCommand()}
	}

	return s.Command.Argv()
}

// runArgv spawns and waits for a process with opened streams.
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

// deadline derives the process context, applying the tool's time limit if set.
func (s *ProcessSpec) deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.Timeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, s.Timeout)
}

// killedBy reports why a process was killed, or nil if it exited normally.
func killedBy(ctx, runCtx context.Context, timeout time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w of %s", ErrToolTimeLimit, timeout)
	}

	return nil
}

// processStreams holds opened files for stdin/stdout/stderr. Nil means not redirected.
type processStreams struct {
	in  *os.File
	out *os.File
	err *os.File
}

// openStreams opens the files named by the spec's redirections.
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

// capture creates files for stdout/stderr capture. Empty path means no capture.
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

// createStream creates a capture file, making parent directories as needed.
func createStream(path string) (*os.File, error) {
	err := os.MkdirAll(filepath.Dir(path), stageDirPerm)
	if err != nil {
		return nil, err
	}

	return os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, stageFilePerm)
}

// attach wires opened files onto the command. Nil fields are left alone (os/exec uses /dev/null).
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

// close closes all opened files, joining errors.
func (s *processStreams) close() error {
	var err error

	for _, file := range []*os.File{s.in, s.out, s.err} {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
	}

	return err
}

// ToolEnvironment builds a clean environment per the CWL spec: HOME, TMPDIR, PATH, plus EnvVarRequirement.
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

	slices.Sort(rendered)

	return rendered, nil
}

// applyEnvVars adds variables from an EnvVarRequirement, evaluating expression values.
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

// envVarDeclares reports whether an EnvVarRequirement explicitly sets the named variable.
func envVarDeclares(scope *cwlcore.RequirementScope, name string) bool {
	declared, found := envVarRequirement(scope)
	if !found {
		return false
	}

	return slices.ContainsFunc(declared.EnvDef, func(definition cwlcore.EnvironmentDef) bool {
		return definition.EnvName == name
	})
}

// withoutInheritedPath drops the inherited PATH (for container use). Keeps EnvVarRequirement-declared PATH.
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

// ToolNetworkAccess reports whether the tool's NetworkAccess requirement allows network access. Default is false.
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

// networkAccess resolves the NetworkAccess requirement in scope.
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

// envVarRequirement resolves the EnvVarRequirement in scope.
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

// ToolTimeLimit returns the tool's time limit, or zero for unlimited.
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

// timeLimitSeconds resolves a timelimit literal or expression to seconds.
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

// toolTimeLimit resolves the ToolTimeLimit in scope.
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
