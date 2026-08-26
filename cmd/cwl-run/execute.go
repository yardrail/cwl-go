package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// reportIndent nests a rendered error tree under the heading naming the
// document it came from.
const reportIndent = "  "

// maxErrorLines is how much of an error tree is printed without -verbose.
// Validating a mapping against an abstract type records why every concrete
// subtype was rejected, which for CWL's twenty-odd requirement classes runs to
// hundreds of lines; the head of the tree holds the answer.
const maxErrorLines = 40

// outDirPerm is the mode a created output directory is given.
const outDirPerm fs.FileMode = 0o755

// emptyJobName is the file name the synthetic empty job order is reported
// against, for a run given no job file. It never exists on disk; it only has to
// be a name a diagnostic can print and a directory relative references can
// resolve against.
const emptyJobName = "-"

// errRun reports a run that neither succeeded nor explained why it did not. It
// is a backstop against a future status this tool has not been taught about
// being silently treated as success.
var errRun = errors.New("the run did not succeed")

// errSuspended reports a run that paused waiting on an external event.
//
// Suspension is this engine's own extension to the specification's status set,
// reached only through a handler a caller registered; cwl-run registers only
// the built-ins, so it cannot happen here today, and the conformance suite
// never suspends. It is still mapped explicitly rather than left to fall
// through, because the two tempting shortcuts are both wrong: exiting 0 with
// the partial outputs would report data the run never finished producing, and
// exiting 33 would tell cwltest the document used a feature we lack, when in
// fact the document is fine and the run is merely unfinished. So it is an
// ordinary failure — exit 1, nothing on stdout — and the message says what is
// waiting.
//
// A suspension is terminal *for this command*. cwl-run has no resume flow: it
// never calls [cwlexec.Runner.Resume], persists no [cwlexec.RunState], and the
// message names the waiting invocations for a human rather than as something
// to feed back in. Driving persist-and-resume is a caller's job, and it stays a
// caller's job inside a subworkflow too — the scheduler never re-dispatches a
// suspended invocation to its handler, so a nested suspension is resumed by
// decoding the payload with [cwlexec.DecodeSubworkflowSuspension], re-running
// the child, and injecting its outputs as a [cwlexec.ResumedStep]. None of that
// belongs behind a cwl-runner command line, which has nowhere to put the state
// between two invocations.
var errSuspended = errors.New("the run suspended and so produced no output object")

// execute loads, runs and reports one CWL document.
//
// Only a successful run writes to stdout. Every diagnostic goes to stderr, so a
// failure leaves stdout empty rather than half an output object.
func execute(ctx context.Context, cfg *config, stdout, stderr io.Writer) error {
	outputs, err := produce(ctx, cfg, stderr)
	if err != nil {
		reportFailure(stderr, cfg, err)

		return err
	}

	return writeOutputs(stdout, outputs)
}

// produce carries a document from a path on the command line to the output
// object of a finished run.
//
// Loading is strict, which is a deliberate choice and not the package default.
// The reference implementation validates strictly unless asked not to
// (cwltool's LoadingContext.strict is true), and for a runner it is the right
// default for a concrete reason: CWL v1.0 and v1.1 type a requirement's class
// as a plain string rather than as a single-symbol enum, so under permissive
// validation a requirement whose fields do not typecheck simply matches some
// other requirement record with an undeclared field. A document that used a
// v1.2 feature under a v1.1 label would run instead of being rejected, which is
// precisely the failure the version routing exists to catch.
//
// A version this engine has no schema for is reported through the cwl-runner
// contract's unsupported status rather than as an invalid document. That check
// now lives inside the loader, so it covers every document a run touches — the
// tools a workflow's steps run included — and not merely the one named here.
func produce(ctx context.Context, cfg *config, stderr io.Writer) (map[string]any, error) {
	process, err := cwlcore.LoadFile(ctx, cfg.process, salad.Strict(true))
	if err != nil {
		return nil, unsupportedVersion(cfg.process, err)
	}

	err = checkCWLVersion(cfg.process, cmp.Or(declaredVersion(cfg.process), process.Base().CWLVersion))
	if err != nil {
		return nil, err
	}

	inputs, err := jobOrder(ctx, cfg, process, stderr)
	if err != nil {
		return nil, err
	}

	settings, err := cfg.execConfig(stderr)
	if err != nil {
		return nil, err
	}

	return runProcess(ctx, process, inputs, settings)
}

// jobOrder builds the input object the process runs against.
//
// Without a job file the process runs against an empty input object, which is
// not the same as running against no input object at all: every declared input
// is still resolved, so one that is neither optional nor defaulted fails here,
// naming the parameter, rather than surfacing later as a tool invoked with a
// missing argument.
// The logger is threaded in because loading reports advisories of its own -- an undeclared job
// key, most of all -- and they belong on the same stream, and under the same --quiet, as every
// other diagnostic this command produces.
func jobOrder(
	ctx context.Context, cfg *config, process cwlcore.Process, stderr io.Writer,
) (map[string]any, error) {
	log := cwlexec.WithJobOrderLogger(cfg.logger(stderr))

	if cfg.job != "" {
		return cwlexec.LoadJobOrder(ctx, cfg.job, process, log)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolving the working directory an empty job order resolves against: %w", err)
	}

	return cwlexec.ParseJobOrder(ctx, filepath.Join(cwd, emptyJobName), []byte("{}"), process, log)
}

// runProcess executes the process and maps the run's status onto this tool's
// contract: an output object, or an error carrying the reason there is none.
//
// The registry and the configuration are handed to the run twice, and both
// hand-offs are necessary. [cwlexec.NewRunner] binds them to the top-level run;
// [cwlexec.WithSubworkflows] is what carries them into a Workflow reached
// through a step's run:. A [cwlexec.StepHandler] is told about its own
// invocation and nothing about the run around it — the right contract for every
// other handler, and the one thing the Workflow handler genuinely needs —​ so
// without the context a nested run falls back to a fresh registry and the zero
// [cwlexec.Config], and every setting resolved out here stops applying one
// level down.
//
// It is passed even though the three settings this command line resolves today
// — the logger, the output directory and the container policy — reach a nested
// run anyway, because the nested Config takes all three from the invocation's
// own [cwlexec.StepCall]. This is the seam that keeps that true for the
// settings that do not: the failure
// policy, the parallelism cap, the expression timeout, the resource budget, the
// requirement policy, and a registry carrying anything other than the built-ins.
// Leaving it out would work now and quietly stop working the first time a flag
// maps onto one of those.
func runProcess(
	ctx context.Context,
	process cwlcore.Process,
	inputs map[string]any,
	settings *cwlexec.Config,
) (map[string]any, error) {
	registry := cwlexec.NewRegistry()

	runner, err := cwlexec.NewRunner(ctx, process, registry, settings)
	if err != nil {
		return nil, err
	}

	result, err := runner.Run(cwlexec.WithSubworkflows(ctx, registry, settings), inputs)
	if err != nil {
		return nil, err
	}

	switch result.Status {
	case cwlexec.StatusSuccess:
		return result.Outputs, nil
	case cwlexec.StatusSuspended:
		return nil, suspendedError(result.Suspensions)
	default:
		// Run reports every failure through its error, so reaching here
		// means a status it grew that this switch has not been taught.
		return nil, fmt.Errorf("%w: it ended with status %q and no explanation", errRun, result.Status)
	}
}

// suspendedError names the invocations a suspended run is waiting on.
func suspendedError(waiting []cwlexec.Suspension) error {
	steps := make([]string, 0, len(waiting))
	for index := range waiting {
		steps = append(steps, waiting[index].StepID)
	}

	if len(steps) == 0 {
		return errSuspended
	}

	return fmt.Errorf("%w; waiting on %s", errSuspended, strings.Join(steps, ", "))
}

// execConfig renders the command line as the configuration a run takes.
func (c *config) execConfig(stderr io.Writer) (*cwlexec.Config, error) {
	outdir, err := c.outputDir()
	if err != nil {
		return nil, err
	}

	return &cwlexec.Config{
		Logger:     c.logger(stderr),
		OutDir:     outdir,
		Containers: c.containerPolicy(),
	}, nil
}

// containerPolicy renders the four container opt-out flags as the policy the
// engine reads.
//
// They are cwltool's, name for name, because a script that runs
// `cwl-runner --no-container` should not have to know which engine is on the
// path. What each one does is [cwlexec.ContainerPolicy]'s to say; all this
// does is carry them, and the zero value — nothing on the command line — is
// the behaviour of an engine that was asked nothing.
func (c *config) containerPolicy() cwlexec.ContainerPolicy {
	return cwlexec.ContainerPolicy{
		Disabled:    c.noContainer,
		NoMatchUser: c.noMatchUser,
		NoReadOnly:  c.noReadOnly,
		Keep:        c.leaveContainer,
	}
}

// outputDir resolves -outdir to an absolute directory, creating it if it does
// not exist.
//
// Absolute is a requirement of the engine, not a preference: every File an
// invocation collects carries a path derived from its output directory, and a
// relative one would tie the run's results to whichever directory this process
// happened to be started in. The default is the working directory, which is
// where a cwl-runner is expected to leave its outputs.
func (c *config) outputDir() (string, error) {
	if c.outdir == "" {
		return os.Getwd()
	}

	abs, err := filepath.Abs(c.outdir)
	if err != nil {
		return "", fmt.Errorf("resolving -outdir %q: %w", c.outdir, err)
	}

	err = os.MkdirAll(abs, outDirPerm)
	if err != nil {
		return "", fmt.Errorf("creating -outdir %q: %w", abs, err)
	}

	return abs, nil
}

// logger builds the logger the engine and its handlers write diagnostics to.
//
// It is built here rather than left to [slog.Default] for two reasons that are
// really one: the default handler writes straight to [os.Stderr], which both
// escapes the writer this tool was given and makes -quiet unable to silence
// anything. -quiet raises the threshold to errors, so advisories go away and a
// failure still gets said out loud.
func (c *config) logger(stderr io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if c.quiet {
		level = slog.LevelError
	}

	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
}

// reportFailure writes a failed run to stderr as a heading naming the document
// followed by the indented error tree, trimmed to a readable length unless
// -verbose asked for all of it.
//
// -quiet does not suppress it. Quiet means the run says nothing while it goes
// well; a failure with no explanation is not a quieter result, it is an
// unusable one.
func reportFailure(stderr io.Writer, cfg *config, err error) {
	limit := maxErrorLines
	if cfg.verbose {
		limit = 0
	}

	shown, omitted := cwlcli.LimitLines(cwlcli.Explain(err), limit)

	fmt.Fprintf(stderr, "%s: FAILED\n%s\n", cfg.process, cwlcli.Indent(shown, reportIndent))

	if omitted > 0 {
		fmt.Fprintf(stderr, "%s... %d more lines; re-run with -verbose for the whole report\n", reportIndent, omitted)
	}
}
