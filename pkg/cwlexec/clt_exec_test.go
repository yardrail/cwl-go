package cwlexec

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// execNowhere is a directory path no test creates, so anything opened under it fails.
const execNowhere = "/proc/self/mem/definitely-not-a-directory"

func TestRunProcessReportsAnEmptyCommandLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		spec *ProcessSpec
		name string
	}{
		{name: "no command at all", spec: &ProcessSpec{}},
		{name: "a command with no arguments", spec: &ProcessSpec{Command: &CommandLine{}}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := RunProcess(t.Context(), testCase.spec)
			if !errors.Is(err, ErrEmptyCommand) {
				t.Errorf("RunProcess: error %v does not wrap %v", err, ErrEmptyCommand)
			}
		})
	}
}

func TestRunProcessShellForm(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	dir := t.TempDir()
	line := &CommandLine{Shell: true, Args: []Arg{
		{Value: execEcho, Quote: true},
		{Value: "a", Quote: true},
		{Value: ">", Quote: false},
		{Value: execOutName, Quote: true},
	}}

	code, err := RunProcess(t.Context(), &ProcessSpec{Command: line, Dir: dir})
	if err != nil || code != 0 {
		t.Fatalf("RunProcess = %d, %v; want 0, nil", code, err)
	}

	if got := execRead(t, filepath.Join(dir, execOutName)); got != "a\n" {
		t.Errorf("the redirection wrote %q, want %q — only a shell can perform it", got, "a\n")
	}
}

func TestRunProcessRedirectionFailures(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execTrue)

	line := &CommandLine{Args: []Arg{{Value: execTrue}}}

	cases := []struct {
		spec *ProcessSpec
		name string
	}{
		{name: "a missing stdin", spec: &ProcessSpec{Command: line, Stdin: execNowhere}},
		{name: "an uncreatable stdout", spec: &ProcessSpec{Command: line, Stdout: execNowhere}},
		{name: "an uncreatable stderr", spec: &ProcessSpec{Command: line, Stderr: execNowhere}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := RunProcess(t.Context(), testCase.spec)
			if err == nil {
				t.Error("RunProcess succeeded with an impossible redirection")
			}
		})
	}
}

func TestRunProcessClosesAnAlreadyOpenedStreamOnFailure(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execTrue)

	dir := t.TempDir()
	spec := &ProcessSpec{
		Command: &CommandLine{Args: []Arg{{Value: execTrue}}},
		Stdout:  filepath.Join(dir, execOutName),
		Stderr:  execNowhere,
	}

	_, err := RunProcess(t.Context(), spec)
	if err == nil {
		t.Fatal("RunProcess succeeded with an impossible stderr")
	}
}

func TestToolEnvironmentWithoutARequirement(t *testing.T) {
	t.Parallel()

	env, err := ToolEnvironment(nil, nil, nil, cwlcore.RuntimeContext{Outdir: pmWork, Tmpdir: pmStage})
	if err != nil {
		t.Fatalf("ToolEnvironment: %v", err)
	}

	want := []string{envHome + "=" + pmWork, envPath + "=" + os.Getenv(envPath), envTmpDir + "=" + pmStage}
	if !slices.Equal(env, want) {
		t.Errorf("env = %q, want %q", env, want)
	}
}

func TestToolEnvironmentOverridesTheNamedVariables(t *testing.T) {
	t.Parallel()

	scope := execScope(&cwlcore.EnvVarRequirement{
		EnvDef: []cwlcore.EnvironmentDef{{EnvName: envHome, EnvValue: "/elsewhere"}},
	})

	env, err := ToolEnvironment(scope, nil, cltEval(), cwlcore.RuntimeContext{Outdir: pmWork})
	if err != nil {
		t.Fatalf("ToolEnvironment: %v", err)
	}

	if !slices.Contains(env, envHome+"=/elsewhere") {
		t.Errorf("env = %q; a declared variable must win over the one the runtime supplies", env)
	}
}

func TestToolTimeLimitResolution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope *cwlcore.RequirementScope
		name  string
		want  time.Duration
	}{
		{name: "no scope at all", scope: nil, want: 0},
		{name: "no requirement", scope: execScope(&cwlcore.ShellCommandRequirement{}), want: 0},
		{
			name:  "a literal limit",
			scope: execScope(&cwlcore.ToolTimeLimit{Timelimit: cwlcore.NewExprLong(90)}),
			want:  90 * time.Second,
		},
		{
			name:  "an unset limit",
			scope: execScope(&cwlcore.ToolTimeLimit{}),
			want:  0,
		},
		{
			name: "an expression limit",
			scope: execScope(&cwlcore.ToolTimeLimit{
				Timelimit: cwlcore.NewExprLongExpression("${return 5;}"),
			}),
			want: 5 * time.Second,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			limit, err := ToolTimeLimit(testCase.scope, nil, cltEval(), cwlcore.RuntimeContext{})
			if err != nil {
				t.Fatalf("ToolTimeLimit: %v", err)
			}

			if limit != testCase.want {
				t.Errorf("limit = %s, want %s", limit, testCase.want)
			}
		})
	}
}

func TestToolTimeLimitFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope *cwlcore.RequirementScope
		want  error
		name  string
	}{
		{
			name: "an expression that is not a number",
			scope: execScope(&cwlcore.ToolTimeLimit{
				Timelimit: cwlcore.NewExprLongExpression("${return 'soon';}"),
			}),
			want: ErrTimeLimitValue,
		},
		{
			name:  "a negative limit",
			scope: execScope(&cwlcore.ToolTimeLimit{Timelimit: cwlcore.NewExprLong(-1)}),
			want:  ErrTimeLimitValue,
		},
		{
			name: "an expression that does not evaluate",
			scope: execScope(&cwlcore.ToolTimeLimit{
				Timelimit: cwlcore.NewExprLongExpression(outBadRef),
			}),
			want: cwlcore.ErrExpressionEval,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := ToolTimeLimit(testCase.scope, nil, cltEval(), cwlcore.RuntimeContext{})
			if !errors.Is(err, testCase.want) {
				t.Errorf("ToolTimeLimit: error %v does not wrap %v", err, testCase.want)
			}
		})
	}
}
