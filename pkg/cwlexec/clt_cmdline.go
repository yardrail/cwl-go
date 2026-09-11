package cwlexec

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Errors reported while assembling a command line. Use [errors.Is] to test.
var (
	// ErrBindingPosition reports a position expression that did not evaluate to an integer.
	ErrBindingPosition = errors.New("command line binding position did not evaluate to an integer")
	// ErrBindingPrefix reports separate: false with no prefix.
	ErrBindingPrefix = errors.New("command line binding requires a prefix")
	// ErrBindingValue reports a value with no command-line rendering.
	ErrBindingValue = errors.New("value cannot be rendered as a command line argument")
	// ErrArgumentValueFrom reports an arguments binding with no valueFrom.
	ErrArgumentValueFrom = errors.New("a CommandLineTool arguments binding requires valueFrom")
)

// shellSafeChars are characters a POSIX shell reads literally (same set as Python's shlex.quote).
const shellSafeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./-_"

// Arg is one element of a built command line.
type Arg struct {
	Value string // argument text, already prefixed and path-resolved
	Quote bool   // effective shellQuote; only meaningful when Shell is true
}

// CommandLine is the built argv for one CommandLineTool invocation.
// Use [CommandLine.Argv] for direct exec, [CommandLine.ShellCommand] for /bin/sh -c.
type CommandLine struct {
	Args  []Arg // baseCommand first, then bound inputs/arguments in sort-key order
	Shell bool  // true when ShellCommandRequirement is in scope
}

// Argv renders the command line as a plain argument vector.
func (c *CommandLine) Argv() []string {
	argv := make([]string, 0, len(c.Args))
	for _, arg := range c.Args {
		argv = append(argv, arg.Value)
	}

	return argv
}

// ShellCommand renders the command line as a single shell string, quoting elements where Quote is true.
func (c *CommandLine) ShellCommand() string {
	parts := make([]string, 0, len(c.Args))

	for _, arg := range c.Args {
		if arg.Quote {
			parts = append(parts, shellQuote(arg.Value))

			continue
		}

		parts = append(parts, arg.Value)
	}

	return strings.Join(parts, " ")
}

// BuildCommandLine assembles the command line for one CommandLineTool invocation.
// inputs is keyed by parameter short name. Neither tool nor inputs is modified.
func BuildCommandLine(tool *cwlcore.CommandLineTool, inputs map[string]any, eval *cwlcore.Evaluator,
	scope *cwlcore.RequirementScope, rt cwlcore.RuntimeContext,
) (*CommandLine, error) {
	if tool == nil {
		return nil, fmt.Errorf("%w: BuildCommandLine needs a CommandLineTool, got nil", ErrWrongProcessClass)
	}

	builder := &cmdBuilder{eval: eval, inputs: inputs, bound: nil, scope: scope, runtime: rt}

	err := builder.collect(tool)
	if err != nil {
		return nil, err
	}

	args, err := builder.assemble(tool.BaseCommand)
	if err != nil {
		return nil, err
	}

	return &CommandLine{Args: args, Shell: shellRequested(scope)}, nil
}

// assemble sorts collected bindings and renders them, prepending baseCommand.
func (b *cmdBuilder) assemble(baseCommand []string) ([]Arg, error) {
	slices.SortStableFunc(b.bound, func(x, y boundArg) int { return compareKeys(x.key, y.key) })

	args := make([]Arg, 0, len(baseCommand)+len(b.bound))
	for _, command := range baseCommand {
		args = append(args, Arg{Value: command, Quote: true})
	}

	for index := range b.bound {
		rendered, err := renderArg(&b.bound[index])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", b.bound[index].origin, err)
		}

		args = append(args, rendered...)
	}

	return args, nil
}

// shellRequested reports whether a ShellCommandRequirement is in scope.
func shellRequested(scope *cwlcore.RequirementScope) bool {
	if scope == nil {
		return false
	}

	_, found, _ := scope.GetRequirement(cwlcore.ClassShellCommandRequirement)

	return found
}

// shellQuote renders text as a single-quoted POSIX shell literal.
func shellQuote(text string) string {
	if text == "" {
		return "''"
	}

	if !strings.ContainsFunc(text, unsafeInShell) {
		return text
	}

	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// unsafeInShell reports whether char requires shell quoting.
func unsafeInShell(char rune) bool {
	return !strings.ContainsRune(shellSafeChars, char)
}
