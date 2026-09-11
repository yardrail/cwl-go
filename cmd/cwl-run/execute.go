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
)

// reportIndent indents error tree lines under the document heading.
const reportIndent = "  "

// maxErrorLines caps the error tree output without -verbose.
const maxErrorLines = 40

// outDirPerm is the mode a created output directory is given.
const outDirPerm fs.FileMode = 0o755

// emptyJobName is the synthetic file name for a run given no job file.
const emptyJobName = "-"

// errRun reports a run that ended with an unhandled status.
var errRun = errors.New("the run did not succeed")

// errSuspended reports a run that paused waiting on an external event.
var errSuspended = errors.New("the run suspended and so produced no output object")

// execute loads, runs and reports one CWL document.
func execute(ctx context.Context, cfg *config, stdout, stderr io.Writer) error {
	outputs, err := produce(ctx, cfg, stderr)
	if err != nil {
		reportFailure(stderr, cfg, err)

		return err
	}

	return writeOutputs(stdout, outputs)
}

// produce loads and runs a CWL document, returning the output object.
func produce(ctx context.Context, cfg *config, stderr io.Writer) (map[string]any, error) {
	process, err := cwlcore.LoadFile(ctx, cfg.process, cwlcore.Strict(true))
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

// jobOrder builds the input object for the run.
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

// runProcess executes the process and returns the output object or an error.
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
		// Unhandled status.
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
		Logger:              c.logger(stderr),
		SelectResources:     nil,
		AllowRequirements:   nil,
		OutDir:              outdir,
		TmpDirPrefix:        "",
		OnError:             "",
		ContainerExecutor:   cwlexec.NewDockerCLIExecutor(),
		Containers:          c.containerPolicy(),
		Resources:           cwlexec.ResourceBudget{Cores: 0, RAMMiB: 0, TmpDirMiB: 0, OutDirMiB: 0},
		EvalTimeout:         0,
		MaxParallel:         0,
		LenientRequirements: false,
	}, nil
}

// containerPolicy maps the container opt-out flags to [cwlexec.ContainerPolicy].
func (c *config) containerPolicy() cwlexec.ContainerPolicy {
	return cwlexec.ContainerPolicy{
		Disabled:    c.noContainer,
		NoMatchUser: c.noMatchUser,
		NoReadOnly:  c.noReadOnly,
		Keep:        c.leaveContainer,
	}
}

// outputDir resolves -outdir to an absolute directory, creating it if needed.
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

// logger builds the logger for engine diagnostics. -quiet raises the level to errors.
func (c *config) logger(stderr io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if c.quiet {
		level = slog.LevelError
	}

	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{AddSource: false, Level: level, ReplaceAttr: nil}))
}

// reportFailure writes the error tree to stderr, trimmed unless -verbose.
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
