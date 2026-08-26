package cwlexec

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The failure paths of the CommandLineTool handler: everything that must stop a run before, during
// or after the tool itself, and the few defensive corners that only a direct call reaches.

func TestCommandLineToolRejectsAnotherClass(t *testing.T) {
	t.Parallel()

	call := execCall(t, nil)
	call.Process = newExpressionTool("$(1)", outID)

	execFail(t, call, ErrWrongProcessClass)
}

func TestCommandLineToolReportsAFailureToStart(t *testing.T) {
	t.Parallel()

	call := execCall(t, execTool([]string{"no-such-program-anywhere"}))

	execFail(t, call, exec.ErrNotFound)
}

func TestCommandLineToolReportsAnEmptyCommandLine(t *testing.T) {
	t.Parallel()

	execFail(t, execCall(t, execTool(nil)), ErrEmptyCommand)
}

func TestCommandLineToolReportsPreparationFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		prepare func(*StepCall)
		want    error
	}{
		{
			name: "an entryname above the working directory",
			prepare: func(call *StepCall) {
				call.Requirements = execScope(direntRequirement("../escape", execGreeting))
			},
			want: ErrStagePath,
		},
		{
			name: "an absolute entryname with no DockerRequirement",
			prepare: func(call *StepCall) {
				call.Requirements = execScope(direntRequirement("/etc/escape", execGreeting))
			},
			want: ErrStagePath,
		},
		{
			name: "an entry with no name to create it under",
			prepare: func(call *StepCall) {
				call.Requirements = execScope(direntRequirement("", execGreeting))
			},
			want: ErrStageEntry,
		},
		{
			name: "a bad stdin expression",
			prepare: func(call *StepCall) {
				execToolOf(call).Stdin = execMissingRef
			},
			want: cwlcore.ErrExpressionEval,
		},
		{
			name: "a bad stdout expression",
			prepare: func(call *StepCall) {
				execToolOf(call).Stdout = execMissingRef
			},
			want: cwlcore.ErrExpressionEval,
		},
		{
			name: "a bad stderr expression",
			prepare: func(call *StepCall) {
				execToolOf(call).Stderr = execMissingRef
			},
			want: cwlcore.ErrExpressionEval,
		},
		{
			name: "a bad environment expression",
			prepare: func(call *StepCall) {
				call.Requirements = execScope(&cwlcore.EnvVarRequirement{
					EnvDef: []cwlcore.EnvironmentDef{{
						EnvName: execEnvName, EnvValue: execMissingRef,
					}},
				})
			},
			want: cwlcore.ErrExpressionEval,
		},
		{
			name: "a bad time limit expression",
			prepare: func(call *StepCall) {
				call.Requirements = execScope(&cwlcore.ToolTimeLimit{
					Timelimit: cwlcore.NewExprLongExpression(execMissingRef),
				})
			},
			want: cwlcore.ErrExpressionEval,
		},
		{
			name: "an unbuildable command line",
			prepare: func(call *StepCall) {
				execToolOf(call).Inputs = []cwlcore.CommandInputParameter{
					cltInput(cltText, cltString, &cwlcore.CommandLineBinding{
						Separate: cwlcore.NewOptBool(false),
					}),
				}
				call.Inputs[cltText] = "v"
			},
			want: ErrBindingPrefix,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			call := execCall(t, execTool([]string{execTrue}))
			testCase.prepare(call)

			execFail(t, call, testCase.want)
		})
	}
}

func TestCommandLineToolReportsAnUnusableOutputJSON(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	tool := execScript("printf 'not json' > cwl.output.json")

	execFail(t, execCall(t, tool), ErrOutputJSON)
}

func TestCommandLineToolInheritsLoadListing(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	// The binding sets no loadListing, so without the requirement the collected Directory's
	// listing stays nil — Process.yml's fallback of no_listing.
	directory := outTestParam(execOutID, outTypeDirectory, outGlobBinding("sub"))
	tool := execScript("mkdir -p sub && echo hi > sub/inner.txt", directory)

	call := execCall(t, tool)
	call.Requirements = execScope(&cwlcore.LoadListingRequirement{LoadListing: cwlcore.LoadListingShallow})

	collected, ok := execSucceed(t, call)[outPort].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("output %q is not a Directory", outPort)
	}

	if len(collected.Listing) != 1 {
		t.Fatalf("listing = %v, want the one entry the LoadListingRequirement asked for", collected.Listing)
	}
}

func TestCommandLineToolMaterializesLiteralsInsideCollections(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execCat)

	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Stdout = execOutName
	tool.Inputs = []cwlcore.CommandInputParameter{
		cltInput(execInPort, cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: cltFile}), cltAt(1)),
	}

	call := execCall(t, tool)
	call.Inputs[execInPort] = []any{
		map[string]any{"nested": execLiteralFile("a.txt", "a\n")},
		execLiteralFile("b.txt", "b\n"),
	}

	// Only the second is bound to the command line; the first proves the walk reaches a literal
	// buried inside a record inside an array.
	call.Inputs[execInPort] = []any{execLiteralFile("a.txt", "a\n"), execLiteralFile("b.txt", "b\n")}

	execWantContent(t, execSucceed(t, call), "a\nb\n")
}

func TestCommandLineToolMaterializesALiteralInsideARecord(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execCat)

	literal := execLiteralFile("a.txt", execGreeting)

	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Stdout = execOutName
	tool.Stdin = "$(inputs.rec.f.path)"

	call := execCall(t, tool)
	call.Inputs["rec"] = map[string]any{"f": literal}

	execWantContent(t, execSucceed(t, call), execGreeting)
}

func TestCommandLineToolRejectsAnUnreachableInput(t *testing.T) {
	t.Parallel()

	unreachable := &cwlcore.File{Basename: execGreetName, Location: "s3://bucket/greet.txt"}

	cases := map[string]any{
		"in a record": map[string]any{"f": unreachable},
		"in an array": []any{unreachable},
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			call := execCall(t, execTool([]string{execTrue}))
			call.Inputs["holder"] = value

			execFail(t, call, ErrUnsupportedFeature)
		})
	}
}

func TestCommandLineToolRejectsARelativeDirectory(t *testing.T) {
	t.Parallel()

	call := execCall(t, execTool([]string{execTrue}))
	call.OutDir = "relative-output"

	execFail(t, call, ErrInvocationDir)
}

func TestCommandLineToolReportsAStagingFailure(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execTrue)

	// The second entry needs the first one's name to be a directory, and it is a file.
	listing := cwlcore.NewInitialWorkDirListing([]cwlcore.InitialWorkDirEntry{
		cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{Entryname: "a", Entry: execGreeting}),
		cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{Entryname: "a/b", Entry: execGreeting}),
	})

	call := execCall(t, execTool([]string{execTrue}))
	call.Requirements = execScope(&cwlcore.InitialWorkDirRequirement{Listing: listing})

	execFail(t, call, nil)
}

func TestCommandLineToolReportsUncreatableDirectories(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execTrue)

	cases := []string{"OutDir", "TmpDir"}

	for _, field := range cases {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			call := execCall(t, execTool([]string{execTrue}))
			blocker := outWriteFile(t, t.TempDir(), "blocker", execGreeting)

			if field == "OutDir" {
				call.OutDir = filepath.Join(blocker, "child")
			} else {
				call.TmpDir = filepath.Join(blocker, "child")
			}

			execFail(t, call, nil)
		})
	}
}

func TestCommandLineToolAcceptsAnEmptyStdinExpression(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execTrue)

	// A stdin expression that produces nothing leaves the tool with no standard input at all,
	// rather than with a file called "".
	tool := execTool([]string{execTrue})
	tool.Stdin = execNullExpr

	execSucceed(t, execCall(t, tool))
}

func TestCommandLineToolLoadListingLeavesExplicitBindingsAlone(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	binding := outGlobBinding("sub")
	binding.LoadListing = cwlcore.LoadListingNone

	tool := execScript("mkdir -p sub && echo hi > sub/inner.txt",
		outTestParam(execOutID, outTypeDirectory, binding),
		outTestParam(extraID, outTypeString, nil))

	call := execCall(t, tool)
	call.Requirements = execScope(&cwlcore.LoadListingRequirement{LoadListing: cwlcore.LoadListingDeep})

	collected, ok := execSucceed(t, call)[outPort].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("output %q is not a Directory", outPort)
	}

	if collected.Listing != nil {
		t.Errorf("listing = %v, want nil: the binding's own no_listing must win", collected.Listing)
	}
}

func TestLoadListingDefaultIgnoresAnUntypedRequirement(t *testing.T) {
	t.Parallel()

	scope := execScope(&cwlcore.RawRequirement{ClassIRI: cwlcore.ClassLoadListingRequirement})

	if mode, found := loadListingDefault(scope); found {
		t.Errorf("loadListingDefault = %q, %v; want it to decline a requirement it cannot read", mode, found)
	}
}

func TestDiscardScratchReportsAFailureWithoutFailingTheRun(t *testing.T) {
	t.Parallel()

	blocker := outWriteFile(t, t.TempDir(), "blocker", execGreeting)
	run := &invocation{call: execCall(t, nil), scratch: filepath.Join(blocker, "child")}

	// A scratch directory that will not go away is worth saying so about; it is not worth
	// failing a tool that has already produced its outputs.
	run.discardScratch()
}

// direntRequirement builds an InitialWorkDirRequirement staging one text Dirent.
func direntRequirement(entryname, entry string) *cwlcore.InitialWorkDirRequirement {
	listing := cwlcore.NewInitialWorkDirListing([]cwlcore.InitialWorkDirEntry{
		cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
			Entryname: cwlcore.Expression(entryname),
			Entry:     cwlcore.Expression(entry),
		}),
	})

	return &cwlcore.InitialWorkDirRequirement{Listing: listing}
}
