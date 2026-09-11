package conformance

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
)

// emptyJobName is the synthetic file name for tests with no job file.
const emptyJobName = "-"

// emptyJobOrder is the input object a test that names no job file runs against.
const emptyJobOrder = "{}"

// errRun reports a run that neither succeeded nor explained why it did not.
var errRun = errors.New("the run did not succeed")

// invocation is one conformance entry resolved onto the filesystem.
type invocation struct {
	process string // CWL document path
	job     string // job order path, or empty
	outDir  string // per-test output directory
	baseDir string // base for relative references
}

// produce loads and runs one conformance entry, returning the output object.
func produce(ctx context.Context, run *invocation) (map[string]any, error) {
	process, err := cwlcore.LoadFile(ctx, run.process, cwlcore.Strict(true))
	if err != nil {
		return nil, unsupportedVersion(run.process, err)
	}

	err = checkCWLVersion(run.process, cmp.Or(declaredVersion(run.process), process.Base().CWLVersion))
	if err != nil {
		return nil, err
	}

	inputs, err := jobOrder(ctx, run, process)
	if err != nil {
		return nil, err
	}

	return execute(ctx, process, inputs, run.outDir)
}

// jobOrder builds the input object for the run.
func jobOrder(ctx context.Context, run *invocation, process cwlcore.Process) (map[string]any, error) {
	if run.job != "" {
		return cwlexec.LoadJobOrder(ctx, run.job, process)
	}

	return cwlexec.ParseJobOrder(ctx, filepath.Join(run.baseDir, emptyJobName), []byte(emptyJobOrder), process)
}

// execute runs the process and returns the output object or an error.
func execute(
	ctx context.Context,
	process cwlcore.Process,
	inputs map[string]any,
	outDir string,
) (map[string]any, error) {
	settings := &cwlexec.Config{
		Logger:            slog.New(slog.DiscardHandler),
		SelectResources:   nil,
		AllowRequirements: nil,
		OutDir:            outDir,
		TmpDirPrefix:      "",
		OnError:           "",
		ContainerExecutor: cwlexec.NewDockerCLIExecutor(),
		Containers: cwlexec.ContainerPolicy{
			Disabled:    false,
			NoMatchUser: false,
			NoReadOnly:  false,
			Keep:        false,
		},
		Resources:           cwlexec.ResourceBudget{Cores: 0, RAMMiB: 0, TmpDirMiB: 0, OutDirMiB: 0},
		EvalTimeout:         0,
		MaxParallel:         0,
		LenientRequirements: false,
	}
	registry := cwlexec.NewRegistry()

	runner, err := cwlexec.NewRunner(ctx, process, registry, settings)
	if err != nil {
		return nil, err
	}

	result, err := runner.Run(cwlexec.WithSubworkflows(ctx, registry, settings), inputs)
	if err != nil {
		return nil, err
	}

	return outputsFromResult(result)
}

// outputsFromResult extracts the output object from a run result.
func outputsFromResult(result cwlexec.RunResult) (map[string]any, error) {
	if result.Status != cwlexec.StatusSuccess {
		return nil, fmt.Errorf("%w: it ended with status %q and no explanation", errRun, result.Status)
	}

	return result.Outputs, nil
}

// outputObject renders outputs in the CWL wire shape via [cwlcore.ToExpressionValue].
func outputObject(outputs map[string]any) map[string]any {
	object := make(map[string]any, len(outputs))

	for name, value := range outputs {
		// Emit null explicitly; a missing key is not the same as null.
		if value == nil {
			object[name] = nil

			continue
		}

		object[name] = cwlcore.ToExpressionValue(value)
	}

	return object
}
