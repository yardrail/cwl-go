package cwlexec

import (
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// shellScope returns the requirement scope of a tool declaring a ShellCommandRequirement.
func cltShellScope() *cwlcore.RequirementScope {
	return cwlcore.NewScope(&cwlcore.CommandLineTool{
		ProcessBase: cwlcore.ProcessBase{
			Requirements: []cwlcore.ProcessRequirement{&cwlcore.ShellCommandRequirement{}},
		},
	})
}

// hintScope returns the scope of a tool declaring a ShellCommandRequirement only as a hint.
func cltHintScope() *cwlcore.RequirementScope {
	return cwlcore.NewScope(&cwlcore.CommandLineTool{
		ProcessBase: cwlcore.ProcessBase{
			Hints: []cwlcore.Hint{&cwlcore.ShellCommandRequirement{}},
		},
	})
}

// pipeTool is the shape every shell test uses: a command whose middle argument is a metacharacter
// the author explicitly asked not to be quoted.
func cltPipeTool() *cwlcore.CommandLineTool {
	return &cwlcore.CommandLineTool{
		BaseCommand: []string{cltEcho},
		Inputs: []cwlcore.CommandInputParameter{
			cltInput(cltText, cltString, cltAt(0)),
		},
		Arguments: []cwlcore.CommandLineArgument{
			cwlcore.NewCommandLineArgumentBinding(&cwlcore.CommandLineBinding{
				Position:   cwlcore.NewExprLong(1),
				ValueFrom:  "|",
				ShellQuote: cwlcore.NewOptBool(false),
			}),
			cwlcore.NewCommandLineArgumentBinding(&cwlcore.CommandLineBinding{
				Position:  cwlcore.NewExprLong(2),
				ValueFrom: "wc -l",
			}),
		},
	}
}

func TestCommandLineShellFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope *cwlcore.RequirementScope
		name  string
		want  bool
	}{
		{name: "there is no scope at all", scope: nil, want: false},
		{name: "the scope is empty", scope: cwlcore.NewScope(nil), want: false},
		{name: "declared as a requirement", scope: cltShellScope(), want: true},
		{name: "declared as a hint", scope: cltHintScope(), want: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			line, err := BuildCommandLine(&cwlcore.CommandLineTool{}, nil, nil,
				testCase.scope, cwlcore.RuntimeContext{})
			if err != nil {
				t.Fatalf("BuildCommandLine: unexpected error: %v", err)
			}

			if line.Shell != testCase.want {
				t.Errorf("Shell = %v, want %v", line.Shell, testCase.want)
			}
		})
	}
}

// TestCommandLineShellQuotability is the seam the executing stream depends on: the argv is the
// same either way, and shellQuote shows up only as a per-element flag.
func TestCommandLineShellQuotability(t *testing.T) {
	t.Parallel()

	inputs := map[string]any{cltText: cltHello}

	withShell, err := BuildCommandLine(cltPipeTool(), inputs, cltEval(), cltShellScope(), cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("BuildCommandLine: unexpected error: %v", err)
	}

	withoutShell, err := BuildCommandLine(cltPipeTool(), inputs, cltEval(), nil, cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("BuildCommandLine: unexpected error: %v", err)
	}

	wantArgv := []string{cltEcho, cltHello, "|", "wc -l"}
	if got := withShell.Argv(); !slices.Equal(got, wantArgv) {
		t.Errorf("argv with ShellCommandRequirement = %q, want %q", got, wantArgv)
	}

	if got := withoutShell.Argv(); !slices.Equal(got, wantArgv) {
		t.Errorf("argv without ShellCommandRequirement = %q, want %q", got, wantArgv)
	}

	wantQuote := []bool{true, true, false, true}
	for index, arg := range withShell.Args {
		if arg.Quote != wantQuote[index] {
			t.Errorf("Args[%d] (%q).Quote = %v, want %v", index, arg.Value, arg.Quote, wantQuote[index])
		}
	}

	// shellQuote is a property of the binding, so it is recorded whether or not a shell will
	// ever act on it. Only Shell says whether it matters.
	for index, arg := range withoutShell.Args {
		if arg.Quote != wantQuote[index] {
			t.Errorf("without shell: Args[%d] (%q).Quote = %v, want %v",
				index, arg.Value, arg.Quote, wantQuote[index])
		}
	}
}

func TestCommandLineShellCommand(t *testing.T) {
	t.Parallel()

	line, err := BuildCommandLine(cltPipeTool(), map[string]any{cltText: cltHello},
		cltEval(), cltShellScope(), cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("BuildCommandLine: unexpected error: %v", err)
	}

	want := `echo 'hello world' | 'wc -l'`
	if got := line.ShellCommand(); got != want {
		t.Errorf("ShellCommand() = %q, want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "the empty string", in: "", want: "''"},
		{name: "already safe", in: "plain-word_1.txt", want: "plain-word_1.txt"},
		{name: "a plain path", in: "/usr/bin/env", want: "/usr/bin/env"},
		{name: "space", in: "two words", want: "'two words'"},
		{name: "pipe", in: "a|b", want: "'a|b'"},
		{name: "dollar", in: "$HOME", want: `'$HOME'`},
		{name: "single quote", in: "it's", want: `'it'\''s'`},
		{name: "newline", in: "a\nb", want: "'a\nb'"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := shellQuote(testCase.in); got != testCase.want {
				t.Errorf("shellQuote(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
		})
	}
}

func TestCommandLineArgvAndShellCommandOnEmpty(t *testing.T) {
	t.Parallel()

	empty := &CommandLine{}

	if got := empty.Argv(); len(got) != 0 {
		t.Errorf("Argv() = %q, want empty", got)
	}

	if got := empty.ShellCommand(); got != "" {
		t.Errorf("ShellCommand() = %q, want empty", got)
	}
}
