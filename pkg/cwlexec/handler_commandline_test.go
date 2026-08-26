package cwlexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The scripts and spellings the handler tests repeat.
const (
	// execWriteScript writes the fixture greeting into the fixture output file.
	execWriteScript = "echo hello > out.txt"

	// execEnvName and execEnvValue are the variable an EnvVarRequirement sets.
	execEnvName  = "CWL_FIXTURE"
	execEnvValue = "set-by-the-document"

	// execLeakName is a variable set in this process's environment that a tool must not see.
	execLeakName = "CWL_MUST_NOT_LEAK"

	// execGreetName is the file the InitialWorkDir fixtures stage.
	execGreetName = "greet.txt"

	// execLongSleep is longer than any test is willing to wait for, so a fixture that reaches
	// the end of it has failed to be killed.
	execLongSleep = "30"

	// execMissingRef is a parameter reference that parses and then fails to resolve, and
	// execNullExpr an expression whose result is an explicit null.
	execMissingRef = "$(inputs.missing.deeper)"
	execNullExpr   = "${return null;}"
)

func TestCommandLineToolRunsAndCollectsAFile(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	call := execCall(t, execScript(execWriteScript, execFileOut(execOutName)))

	outputs := execSucceed(t, call)
	execWantContent(t, outputs, execGreeting)

	file := execOutFile(t, outputs)
	if filepath.Dir(file.Path) != call.OutDir {
		t.Errorf("output is at %q, want it inside the allocated output directory %q", file.Path, call.OutDir)
	}
}

func TestCommandLineToolAllocatesItsOwnDirectories(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	// A bare tool driven through a zero Config gets no directories at all; the handler has to
	// make its own, and the outputs must still be somewhere absolute.
	call := execCall(t, execScript(execWriteScript, execFileOut(execOutName)))
	call.OutDir, call.TmpDir = "", ""

	file := execOutFile(t, execSucceed(t, call))

	if !filepath.IsAbs(file.Path) {
		t.Fatalf("output path %q is not absolute", file.Path)
	}

	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(file.Path)) })

	if got := execRead(t, file.Path); got != execGreeting {
		t.Errorf("output holds %q, want %q", got, execGreeting)
	}
}

func TestCommandLineToolCapturesStdout(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execEcho)

	tool := execTool([]string{execEcho, jobHello}, execFileOut(execOutName))
	tool.Stdout = execOutName

	execWantContent(t, execSucceed(t, execCall(t, tool)), execGreeting)
}

func TestCommandLineToolCapturesAnUnnamedStdout(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execEcho)

	// No `stdout:` field, so the filename is whatever StreamFile derives — and the collector
	// has to glob for the same one without being told what it was.
	shortcut := outTestParam(execOutID, cwlcore.NewShortcutType(cwlcore.TypeKindStdout), nil)
	tool := execTool([]string{execEcho, jobHello}, shortcut)

	execWantContent(t, execSucceed(t, execCall(t, tool)), execGreeting)

	derived, err := StreamFile(tool, StreamStdout, nil, nil, cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("StreamFile: %v", err)
	}

	file := execOutFile(t, execSucceed(t, execCall(t, tool)))
	if file.Basename != derived {
		t.Errorf("captured to %q, want the name StreamFile derives, %q", file.Basename, derived)
	}
}

func TestCommandLineToolCapturesStderr(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	shortcut := outTestParam(execOutID, cwlcore.NewShortcutType(cwlcore.TypeKindStderr), nil)
	tool := execScript("echo hello 1>&2", shortcut)
	tool.Stderr = "log.err"

	execWantContent(t, execSucceed(t, execCall(t, tool)), execGreeting)
}

func TestCommandLineToolDiscardsAnUncapturedStream(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	// Nothing declares stdout, so nothing may appear in the output directory: a stray capture
	// file would be collected by any glob the document writes.
	call := execCall(t, execScript("echo noise; echo more 1>&2"))

	execSucceed(t, call)

	entries, err := os.ReadDir(call.OutDir)
	if err != nil {
		t.Fatalf("reading the output directory: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("output directory holds %d entries, want none", len(entries))
	}
}

func TestCommandLineToolRedirectsStdin(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execCat)

	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Stdin = "$(inputs.f.path)"
	tool.Stdout = execOutName

	call := execCall(t, tool)
	call.Inputs[execInPort] = execSourceFile(t, execGreeting)

	execWantContent(t, execSucceed(t, call), execGreeting)
}

func TestCommandLineToolRedirectsStdinThroughTheTypeShortcut(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execCat)

	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Stdout = execOutName
	tool.Inputs = []cwlcore.CommandInputParameter{
		cltInput(execInPort, cwlcore.NewShortcutType(cwlcore.TypeKindStdin), nil),
	}

	call := execCall(t, tool)
	call.Inputs[execInPort] = execSourceFile(t, execGreeting)

	execWantContent(t, execSucceed(t, call), execGreeting)
}

func TestCommandLineToolExitCodeClassification(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execFalse)

	cases := []struct {
		name    string
		program string
		codes   func(*cwlcore.CommandLineTool)
		want    Status
	}{
		{
			name:    "declared success code",
			program: execFalse,
			codes:   func(tool *cwlcore.CommandLineTool) { tool.SuccessCodes = []int{1} },
			want:    StatusSuccess,
		},
		{
			name:    "declared temporary failure",
			program: execFalse,
			codes:   func(tool *cwlcore.CommandLineTool) { tool.TemporaryFailCodes = []int{1} },
			want:    StatusTemporaryFail,
		},
		{
			name:    "declared permanent failure",
			program: execFalse,
			codes:   func(tool *cwlcore.CommandLineTool) { tool.PermanentFailCodes = []int{1} },
			want:    StatusPermanentFail,
		},
		{
			name:    "undeclared non-zero exit",
			program: execFalse,
			codes:   func(*cwlcore.CommandLineTool) {},
			want:    StatusPermanentFail,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tool := execTool([]string{testCase.program})
			testCase.codes(tool)

			if testCase.want == StatusSuccess {
				execSucceed(t, execCall(t, tool))

				return
			}

			if got := execFail(t, execCall(t, tool), ErrToolExit); got != testCase.want {
				t.Errorf("status = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestCommandLineToolTimeLimitKillsTheProcess(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execSleep)

	call := execCall(t, execTool([]string{execSleep, execLongSleep}))
	call.Requirements = execScope(&cwlcore.ToolTimeLimit{Timelimit: cwlcore.NewExprLong(1)})

	started := time.Now()

	execFail(t, call, ErrToolTimeLimit)

	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Errorf("the tool ran for %s; it should have been killed after a second", elapsed)
	}
}

func TestCommandLineToolContextCancellationKillsTheProcess(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execSleep)

	call := execCall(t, execTool([]string{execSleep, execLongSleep}))

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)

	handler, _ := NewRegistry().Handler(Class(cwlcore.ClassCommandLineTool))

	started := time.Now()

	result, err := Outcome(handler.Execute(ctx, call))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	if result.Status != StatusPermanentFail {
		t.Errorf("status = %q, want a permanent failure", result.Status)
	}

	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Errorf("the tool ran for %s; cancelling should have killed it", elapsed)
	}
}

func TestCommandLineToolEnvironmentIsNotInherited(t *testing.T) {
	// Not parallel: t.Setenv mutates this process's environment, which is the very thing the
	// tool must not inherit.
	execSkipUnless(t, execShell)

	t.Setenv(execLeakName, "leaked")

	script := "echo $" + execEnvName + " $" + execLeakName + " ${HOME} ${TMPDIR}"
	tool := execScript(script, execFileOut(execOutName))
	tool.Stdout = execOutName

	call := execCall(t, tool)
	call.Requirements = execScope(&cwlcore.EnvVarRequirement{
		EnvDef: []cwlcore.EnvironmentDef{{EnvName: execEnvName, EnvValue: cwlcore.Expression(execEnvValue)}},
	})

	file := execOutFile(t, execSucceed(t, call))
	fields := strings.Fields(execRead(t, file.Path))

	want := []string{execEnvValue, call.OutDir, call.TmpDir}
	if !slices.Equal(fields, want) {
		t.Errorf("the tool saw %q, want %q — the parent's %s must not be inherited",
			fields, want, execLeakName)
	}
}

func TestCommandLineToolShellCommandRequirement(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	// "&&" only chains two commands if a shell interprets it, and it only reaches the shell
	// unquoted because the binding says shellQuote: false.
	chain := &cwlcore.CommandLineBinding{
		ValueFrom:  "&&",
		Position:   cwlcore.NewExprLong(1),
		ShellQuote: cwlcore.NewOptBool(false),
	}

	tool := execTool([]string{execEcho, "a"}, execFileOut(execOutName))
	tool.Arguments = []cwlcore.CommandLineArgument{
		cwlcore.NewCommandLineArgumentBinding(chain),
		cwlcore.NewCommandLineArgumentBinding(&cwlcore.CommandLineBinding{
			ValueFrom: cwlcore.Expression(execEcho), Position: cwlcore.NewExprLong(2),
		}),
		cwlcore.NewCommandLineArgumentBinding(&cwlcore.CommandLineBinding{
			ValueFrom: "b", Position: cwlcore.NewExprLong(3),
		}),
	}
	tool.Stdout = execOutName

	call := execCall(t, tool)
	call.Requirements = execScope(&cwlcore.ShellCommandRequirement{})

	execWantContent(t, execSucceed(t, call), "a\nb\n")
}

func TestCommandLineToolWithoutShellCommandRequirement(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execEcho)

	// The same argv with no requirement in scope: "&&" is one more word for echo to print,
	// because no shell ever sees it.
	tool := execTool([]string{execEcho, "a", "&&", execEcho, "b"}, execFileOut(execOutName))
	tool.Stdout = execOutName

	execWantContent(t, execSucceed(t, execCall(t, tool)), "a && echo b\n")
}

func TestCommandLineToolInitialWorkDir(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execCat)

	source := execSourceFile(t, "staged\n")

	listing := cwlcore.NewInitialWorkDirListing([]cwlcore.InitialWorkDirEntry{
		cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
			Entryname: cwlcore.Expression(execGreetName),
			Entry:     cwlcore.Expression(execGreeting),
		}),
		cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
			Entryname: "$(inputs.f.basename)",
			Entry:     "$(inputs.f)",
		}),
	})

	tool := execTool([]string{execCat, execGreetName, execSourceName}, execFileOut(execOutName))
	tool.Stdout = execOutName

	call := execCall(t, tool)
	call.Inputs[execInPort] = source
	call.Requirements = execScope(&cwlcore.InitialWorkDirRequirement{Listing: listing})

	execWantContent(t, execSucceed(t, call), execGreeting+"staged\n")

	// The staged input must be reachable at its staged location, not at the host one.
	staged := filepath.Join(call.OutDir, execSourceName)
	if got := execRead(t, staged); got != "staged\n" {
		t.Errorf("%s holds %q, want the input's content", staged, got)
	}
}

func TestCommandLineToolStagesAFileLiteral(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execCat)

	listing := cwlcore.NewInitialWorkDirListing([]cwlcore.InitialWorkDirEntry{
		cwlcore.NewInitialWorkDirFile(execLiteralFile(execGreetName, execGreeting)),
	})

	tool := execTool([]string{execCat, execGreetName}, execFileOut(execOutName))
	tool.Stdout = execOutName

	call := execCall(t, tool)
	call.Requirements = execScope(&cwlcore.InitialWorkDirRequirement{Listing: listing})

	execWantContent(t, execSucceed(t, call), execGreeting)
}

func TestCommandLineToolMaterializesALiteralInput(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execCat)

	// The literal is never staged into the working directory, so it must be written somewhere
	// the tool can still open — and the argv must name that place.
	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Stdout = execOutName
	tool.Inputs = []cwlcore.CommandInputParameter{cltInput(execInPort, cltFile, cltAt(1))}

	call := execCall(t, tool)
	call.Inputs[execInPort] = execLiteralFile(execGreetName, execGreeting)

	execWantContent(t, execSucceed(t, call), execGreeting)

	_, err := os.Stat(filepath.Join(call.OutDir, execGreetName))
	if err == nil {
		t.Error("the literal was written into the output directory, where an output glob would collect it")
	}
}

func TestCommandLineToolBindsExpressionsOverFileProperties(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execEcho)

	// The whole chain in one row: a staged input File reaches the argv builder as a typed value,
	// an expression reads a property off it, and the tool sees the *staged* name rather than the
	// one the file had on the host.
	binding := cltAt(1)
	binding.ValueFrom = "$(self.basename) in $(runtime.outdir)"

	tool := execTool([]string{execEcho}, execFileOut(execOutName))
	tool.Stdout = execOutName
	tool.Inputs = []cwlcore.CommandInputParameter{cltInput(execInPort, cltFile, binding)}

	listing := cwlcore.NewInitialWorkDirListing([]cwlcore.InitialWorkDirEntry{
		cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
			Entryname: cwlcore.Expression(execGreetName), Entry: "$(inputs.f)",
		}),
	})

	call := execCall(t, tool)
	call.Inputs[execInPort] = execSourceFile(t, execGreeting)
	call.Requirements = execScope(&cwlcore.InitialWorkDirRequirement{Listing: listing})

	execWantContent(t, execSucceed(t, call), execGreetName+" in "+call.OutDir+"\n")
}

// execPairDef declares a record type by name, the way a workflow-level SchemaDefRequirement does.
// Its fields carry output bindings of their own, which is what a record output is collected through.
const execPairDef = `
- name: pair
  type: record
  fields:
    - name: text
      type: File
      outputBinding:
        glob: made.txt
    - name: tree
      type: Directory
      outputBinding:
        glob: sub
`

// execMakePair is the script that produces what execPairDef describes.
const execMakePair = "echo hello > made.txt && mkdir -p sub && echo hi > sub/inner.txt"

func TestCommandLineToolResolvesAnInheritedSchemaDef(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	// The tool declares its output by a name only the *workflow* defines. CollectOutputs builds
	// its own scope from the tool alone, so nothing but the handler can resolve this.
	tool := execScript(execMakePair, outTestParam(execOutID, cwlcore.NewNamedType("pair"), nil))

	call := execCall(t, tool)
	call.Requirements = execInheritedScope(t, execSchemaDef(t, execPairDef))

	collected := execSucceed(t, call)[outPort]

	record, ok := collected.(map[string]any)
	if !ok {
		t.Fatalf("output %q = %#v, want a record", outPort, collected)
	}

	file, ok := record["text"].(*cwlcore.File)
	if !ok {
		t.Fatalf("field %q = %#v, want a File", "text", record["text"])
	}

	if file.Checksum != outSumHello {
		t.Errorf("checksum = %q, want %q", file.Checksum, outSumHello)
	}
}

func TestCommandLineToolLoadListingReachesRecordFields(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	// A record field's Directory is subject to the same loadListing precedence as a top-level
	// output, and the array and union members exercise the rest of the walk.
	dirs := cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: outTypeDirectory})
	maybe := cwlcore.NewUnionType([]cwlcore.TypeRef{outTypeNull, outTypeDirectory})

	declared := outRecord(
		outField("tree", outTypeDirectory, outGlobBinding("sub")),
		outField("trees", dirs, outGlobBinding("sub")),
		outField("maybe", maybe, outGlobBinding("sub")),
	)

	tool := execScript(execMakePair, outTestParam(execOutID, declared, nil))

	call := execCall(t, tool)
	call.Requirements = execScope(&cwlcore.LoadListingRequirement{LoadListing: cwlcore.LoadListingShallow})

	record, ok := execSucceed(t, call)[outPort].(map[string]any)
	if !ok {
		t.Fatalf("output %q is not a record", outPort)
	}

	dir, ok := record["tree"].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("field %q = %#v, want a Directory", "tree", record["tree"])
	}

	if len(dir.Listing) != 1 {
		t.Errorf("listing = %v, want the one entry the LoadListingRequirement asked for", dir.Listing)
	}
}

func TestRelistTypeLeavesAnEmptySchemaAlone(t *testing.T) {
	t.Parallel()

	// A type whose kind promises a schema and whose payload has none is returned untouched
	// rather than rebuilt around nothing.
	cases := map[string]cwlcore.TypeRef{
		"a record with no schema": cwlcore.NewRecordType(nil),
		"an array with no schema": cwlcore.NewArrayType(nil),
	}

	for name, declared := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := relistType(declared, cwlcore.LoadListingDeep); got != declared {
				t.Errorf("relistType = %v, want it unchanged", got)
			}
		})
	}
}

func TestCommandLineToolOutputJSONBypassesBinding(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	// The glob would find nothing, so a non-null output proves the file was used instead.
	script := `printf '{"out": {"class": "File", "path": "made.txt"}}' > cwl.output.json; echo hi > made.txt`
	tool := execScript(script, execFileOut("never-matches-*"))

	file := execOutFile(t, execSucceed(t, execCall(t, tool)))

	if file.Basename != "made.txt" {
		t.Errorf("basename = %q, want the one cwl.output.json named", file.Basename)
	}

	if !file.Size.IsSet() || file.Checksum == "" {
		t.Errorf("size = %s, checksum = %q, want both derived from disk", file.Size, file.Checksum)
	}
}

func TestCommandLineToolDockerRequirementIsUnsupported(t *testing.T) {
	t.Parallel()

	call := execCall(t, execTool([]string{execTrue}))
	call.Requirements = execScope(&cwlcore.DockerRequirement{DockerPull: "debian:stable"})

	if got := execFail(t, call, ErrUnsupportedFeature); got != StatusPermanentFail {
		t.Errorf("status = %q, want a permanent failure", got)
	}
}

func TestCommandLineToolDockerHintRuns(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execTrue)

	// A hint is advisory: "an implementation may ignore a hint". Failing on one would make an
	// ordinary document unrunnable on a host that has no container engine.
	call := execCall(t, execTool([]string{execTrue}))
	call.Requirements = execHintScope(&cwlcore.DockerRequirement{DockerPull: "debian:stable"})

	execSucceed(t, call)
}

func TestCommandLineToolToleratesDeclarativeRequirements(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execTrue)

	// NetworkAccess and WorkReuse are recorded by the document and need nothing of a host
	// executor: the tool has whatever network this process has, and nothing here caches a
	// previous run's results, so declining reuse is honoured by construction.
	call := execCall(t, execTool([]string{execTrue}))
	call.Requirements = execScope(
		&cwlcore.NetworkAccess{NetworkAccess: cwlcore.NewExprBool(true)},
		&cwlcore.WorkReuse{EnableReuse: cwlcore.NewExprBool(false)},
	)

	execSucceed(t, call)
}
