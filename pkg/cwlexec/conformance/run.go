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

// emptyJobName is the file name the synthetic empty job order is reported against, for a
// test that names no job file. It never exists on disk; it only has to be a name a
// diagnostic can print and a directory relative references can resolve against.
const emptyJobName = "-"

// emptyJobOrder is the input object a test that names no job file runs against.
const emptyJobOrder = "{}"

// errRun reports a run that neither succeeded nor explained why it did not.
var errRun = errors.New("the run did not succeed")

// invocation is one conformance entry resolved onto the filesystem: the two documents to
// run and the directory the run writes its outputs under.
type invocation struct {
	// process is the absolute path of the CWL document to run.
	process string
	// job is the absolute path of the job order, empty when the entry names none.
	job string
	// outDir is the directory the run's output files are written under. cwltest gives
	// each test a fresh temporary directory and never reuses one, so this does too.
	outDir string
	// baseDir is the directory relative references in a synthetic empty job order
	// resolve against. cwl-run uses its working directory and cwltest runs it from the
	// corpus root, so that is what this is.
	baseDir string
}

// produce carries one conformance entry from its documents to the output object of a
// finished run, by the same route cmd/cwl-run takes.
//
// Loading is strict, which is the command's choice and not the package default: CWL v1.0
// and v1.1 type a requirement's class as a plain string rather than as a single-symbol
// enum, so under permissive validation a requirement whose fields do not typecheck simply
// matches some other requirement record with an undeclared field, and a document using a
// v1.2 feature under a v1.1 label would run instead of being rejected.
//
// A version there is no schema for is reported through the contract's unsupported status
// rather than as an invalid document. That check lives inside the loader, so it covers
// every document a run touches and not merely the one named here.
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

// jobOrder builds the input object the process runs against.
//
// A test that names no job file runs against an empty input object, which is not the same
// as running against no input object at all: every declared input is still resolved, so
// one that is neither optional nor defaulted fails here, naming the parameter.
func jobOrder(ctx context.Context, run *invocation, process cwlcore.Process) (map[string]any, error) {
	if run.job != "" {
		return cwlexec.LoadJobOrder(ctx, run.job, process)
	}

	return cwlexec.ParseJobOrder(ctx, filepath.Join(run.baseDir, emptyJobName), []byte(emptyJobOrder), process)
}

// execute runs the process and maps the run's status onto an output object or the reason
// there is none.
//
// The registry and the configuration are bound twice over, exactly as cmd/cwl-run binds
// them: [cwlexec.NewRunner] binds them to the top-level run, and
// [cwlexec.WithSubworkflows] is what carries them into a Workflow reached through a step's
// run:. Without the second, a nested run falls back to a fresh registry and the zero
// [cwlexec.Config].
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

// outputsFromResult maps a finished run's status onto its output object, or the reason
// there is none.
//
// It is split out of execute so the StatusSuspended (and, in principle, any other
// non-success) branch can be unit-tested directly against a hand-built
// [cwlexec.RunResult] -- execute always builds its own default [cwlexec.NewRegistry], and
// no built-in handler ever leaves a top-level run suspended, so the branch cannot be
// reached by running the engine at all.
func outputsFromResult(result cwlexec.RunResult) (map[string]any, error) {
	if result.Status != cwlexec.StatusSuccess {
		// A suspension is the only status that reaches here today, and it is an
		// ordinary failure: the document is fine and the run is merely unfinished,
		// so reporting it as an unsupported feature would credit the engine with a
		// skip it has not earned.
		return nil, fmt.Errorf("%w: it ended with status %q and no explanation", errRun, result.Status)
	}

	return result.Outputs, nil
}

// outputObject renders a finished run's outputs in the wire shape cwltest compares,
// exactly as cmd/cwl-run writes it to stdout.
//
// The values arrive typed -- a File is a *cwlcore.File, not a map -- and
// [cwlcore.ToExpressionValue] is what turns them back into the objects a CWL document
// writes. It is used rather than a converter of this harness's own precisely because it
// already draws the distinction that is impossible to see afterwards: absent is not zero.
// An unmeasured size is omitted while an explicit 0 is emitted, and a nil directory
// listing -- which means "not read", not "empty" -- is omitted rather than written as [].
func outputObject(outputs map[string]any) map[string]any {
	object := make(map[string]any, len(outputs))

	for name, value := range outputs {
		// A null output -- an unwired workflow output, or one a skipped step never
		// produced -- is emitted as null rather than dropped: the specification
		// gives every declared output parameter a value, and a missing key is not
		// the same answer as an explicit null.
		if value == nil {
			object[name] = nil

			continue
		}

		object[name] = cwlcore.ToExpressionValue(value)
	}

	return object
}
