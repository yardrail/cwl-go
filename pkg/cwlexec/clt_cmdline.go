package cwlexec

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Errors reported while assembling a CommandLineTool's command line. They are wrapped with the
// parameter or argument index they came from, so callers should test them with [errors.Is].
var (
	// ErrBindingPosition reports a CommandLineBinding whose `position` expression did not
	// evaluate to an integer. The schema is explicit that "expressions must return a single
	// value of type int or a null"; a null is read as the schema default of 0, anything else
	// is this error.
	ErrBindingPosition = errors.New("command line binding position did not evaluate to an integer")

	// ErrBindingPrefix reports a binding declaring `separate: false` and no `prefix`, since
	// `separate` says only how a prefix and its value are joined and means nothing without one.
	//
	// A true boolean with no prefix is deliberately not this error; it adds nothing to the
	// command line. See [renderTrue] for why, and for what that costs.
	ErrBindingPrefix = errors.New("command line binding requires a prefix")

	// ErrBindingValue reports a value that has no command-line rendering: a File or Directory
	// with no `path`, or a value of a type the binding rules do not cover.
	ErrBindingValue = errors.New("value cannot be rendered as a command line argument")

	// ErrArgumentValueFrom reports a CommandLineTool.arguments entry written as a
	// CommandLineBinding with no `valueFrom`, which the specification requires of a binding
	// that is part of the CommandLineTool.arguments field.
	ErrArgumentValueFrom = errors.New("a CommandLineTool arguments binding requires valueFrom")
)

// shellSafeChars are the characters a POSIX shell reads literally, so that an argument built only
// from them needs no quoting. It is the set Python's shlex.quote uses, which is what the reference
// implementation quotes with.
const shellSafeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./-_"

// Arg is one element of a built command line: the text itself, plus whether a shell that runs the
// command must quote it.
//
// Quote only means anything when the enclosing [CommandLine] has Shell set. Without a
// ShellCommandRequirement the command is not interpreted by a shell at all, `shellQuote` is
// irrelevant, and every Arg is passed to the operating system exactly as it stands.
type Arg struct {
	// Value is the argument text, already prefixed, joined and path-resolved.
	Value string

	// Quote is the binding's effective `shellQuote`, whose schema default is true. False marks
	// an element the author deliberately wants the shell to interpret — "|", ">", "&&" — and
	// which must therefore be spliced into the command string unquoted.
	Quote bool
}

// CommandLine is the result of [BuildCommandLine]: the whole argv of one CommandLineTool
// invocation, and whether it is to be run through a shell.
//
// The two cases are genuinely different invocations, and the distinction is deliberately left to
// the executing stream rather than baked into the argv here:
//
//   - Shell is false. Run Args[0] with Args[1:] as its arguments; there is no shell, so no
//     quoting happens and [Arg.Quote] carries no meaning. Use [CommandLine.Argv].
//   - Shell is true. A ShellCommandRequirement is in scope. The specification's rule is that each
//     item "must be joined into a string separated by single spaces and quoted to prevent
//     interpretation by the shell, unless CommandLineBinding for that argument contains
//     shellQuote: false". [CommandLine.ShellCommand] applies exactly that rule; run the result
//     as `/bin/sh -c <string>`.
//
// Redirections are not part of a CommandLine. A tool's `stdin`, `stdout` and `stderr` are
// expression-bearing filenames that the executing stream evaluates and wires up as file
// descriptors on the process it spawns — never as argv elements, and never as shell redirection
// operators spliced into ShellCommand, which would change their meaning under `shellQuote: false`.
type CommandLine struct {
	// Args are the command line's elements in order: [CommandLineTool.BaseCommand] first, then
	// the bound inputs and `arguments` interleaved in sort-key order.
	Args []Arg

	// Shell reports whether a ShellCommandRequirement is in scope for this invocation.
	Shell bool
}

// Argv renders the command line as a plain argument vector, discarding the per-element quoting
// flags. It is what a caller execs directly when [CommandLine.Shell] is false.
func (c *CommandLine) Argv() []string {
	argv := make([]string, 0, len(c.Args))
	for _, arg := range c.Args {
		argv = append(argv, arg.Value)
	}

	return argv
}

// ShellCommand renders the command line as the single string a shell is handed with -c, quoting
// every element whose [Arg.Quote] is true and splicing the rest in verbatim.
//
// It is meaningful only when [CommandLine.Shell] is true; on a command line built without a
// ShellCommandRequirement it still renders, but running the result through a shell would be a
// change of semantics rather than a formatting choice.
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

// BuildCommandLine assembles the command line for one CommandLineTool invocation from the tool and
// its already-resolved input object.
//
// The algorithm is the specification's, in its six steps: collect the bindings from `arguments`
// and from the `inputs` schema, give each a sort key, sort, render each binding by the rules of
// CommandLineBinding, and put `baseCommand` at the front. inputs is keyed by parameter short name,
// as [ShortName] derives it and as [StepCall.Inputs] carries it.
//
// eval evaluates the expressions a binding may carry — `position` and `valueFrom` — with `self`
// bound to the value being bound, `inputs` to the whole input object and `runtime` to rt.
//
// scope is read for exactly two things: the SchemaDefRequirement that resolves a parameter typed by
// name, and the ShellCommandRequirement that sets [CommandLine.Shell]. Both a nil evaluator and a
// nil scope are valid: they mean parameter references only, and no requirement in scope.
//
// File and Directory values are rendered as their `path`, per the binding rules, and are accepted
// either as *[cwlcore.File] / *[cwlcore.Directory] or as the map form a job order decodes to. A
// value carrying only a `location` is an error: path assignment is the staging stream's job, and
// it must have happened before a command line can name the file.
//
// What this function deliberately does not do: it stages nothing, spawns nothing, reads nothing
// from disk, and honours no redirection. In particular `loadContents` is not applied here — a
// File's `contents` must already be populated by the time an expression can read it — and
// `stdin`/`stdout`/`stderr` are not argv and are left to the executing stream. See [CommandLine].
//
// Neither tool nor inputs is modified, so concurrent calls may share both. That matters because a
// scattered step's sub-jobs run concurrently over one input object; see [StepCall.Inputs].
func BuildCommandLine(tool *cwlcore.CommandLineTool, inputs map[string]any, eval *cwlcore.Evaluator,
	scope *cwlcore.RequirementScope, rt cwlcore.RuntimeContext,
) (*CommandLine, error) {
	if tool == nil {
		return nil, fmt.Errorf("%w: BuildCommandLine needs a CommandLineTool, got nil", ErrWrongProcessClass)
	}

	builder := &cmdBuilder{eval: eval, inputs: inputs, scope: scope, runtime: rt}

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

// assemble sorts the collected bindings and renders them, with baseCommand inserted in front.
//
// Spec step 6 is "Insert elements from `baseCommand` at the beginning of the command line", so
// baseCommand is prepended rather than sorted. The reference implementation instead gives each
// baseCommand element the sort key [-1000000, index] and lets it sort, which a document declaring
// a position below that magic number would defeat; the spec's rule cannot be.
//
// The sort is stable, so bindings whose keys are wholly equal keep the order they were collected
// in: input parameters in document order, then `arguments` in document order.
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

// shellRequested reports whether a ShellCommandRequirement is in scope. A declaration in hints
// counts, which is what [cwlcore.RequirementScope.GetRequirement] already resolves and what the
// reference implementation does.
func shellRequested(scope *cwlcore.RequirementScope) bool {
	if scope == nil {
		return false
	}

	_, found, _ := scope.GetRequirement(cwlcore.ClassShellCommandRequirement)

	return found
}

// shellQuote renders text so a POSIX shell reads it as one literal word, using the single-quote
// form: everything inside single quotes is literal, and an embedded quote is closed, escaped and
// reopened.
func shellQuote(text string) string {
	if text == "" {
		return "''"
	}

	if !strings.ContainsFunc(text, unsafeInShell) {
		return text
	}

	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// unsafeInShell reports whether char is one a shell might interpret, and so one that forces the
// whole word to be quoted.
func unsafeInShell(char rune) bool {
	return !strings.ContainsRune(shellSafeChars, char)
}
