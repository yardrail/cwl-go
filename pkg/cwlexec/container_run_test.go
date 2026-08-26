package cwlexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The end-to-end container tests. They need a reachable engine that can bind-mount this machine's
// filesystem, and they fail rather than skip without one: a container test that skips itself
// reports the same thing whether the feature works or was never exercised, which is the one thing a
// conformance-driven project cannot afford to be told.

// ctrRunCall builds a call whose tool runs inside a container, with any extra requirements the row
// needs declared alongside the DockerRequirement.
func ctrRunCall(t *testing.T, tool *cwlcore.CommandLineTool,
	declared *cwlcore.DockerRequirement, extra ...cwlcore.ProcessRequirement,
) *StepCall {
	t.Helper()

	call := execCall(t, tool)
	call.Requirements = execScope(append([]cwlcore.ProcessRequirement{declared}, extra...)...)

	return call
}

// ctrPulled is the requirement every row starts from: a small, portable image.
func ctrPulled() *cwlcore.DockerRequirement {
	return &cwlcore.DockerRequirement{DockerPull: ctrImage}
}

// ctrWriteTool is the tool the two "the container really wrote this" rows run: a shell one-liner
// that puts execGreeting into execOutName. It is shared so that
// TestContainerScriptIsOneShellCommand pins the script both of them depend on.
func ctrWriteTool() *cwlcore.CommandLineTool {
	// execWriteScript rather than an interpolation of execGreeting: the greeting ends in the
	// newline `echo` itself supplies, and a newline inside a -c argument starts a second command.
	return execScript(execWriteScript, execFileOut(execOutName))
}

func TestToolRunsInContainer(t *testing.T) {
	t.Parallel()

	outputs := execSucceed(t, ctrRunCall(t, ctrWriteTool(), ctrPulled()))

	execWantContent(t, outputs, execGreeting)

	// The checksum is measured from the file on this host, so a value that reads correctly is a
	// value the container really wrote through the mount rather than one this process invented.
	file := execOutFile(t, outputs)
	if file.Checksum == "" || !file.Size.IsSet() {
		t.Errorf("checksum = %q, size = %s; want both measured from disk", file.Checksum, file.Size)
	}
}

func TestContainerOutputsAreNotRootOwned(t *testing.T) {
	t.Parallel()

	// The concrete reason --user exists. The image's user is root, and a file root wrote into a
	// bind mount is owned by root on this host: this process could neither read it back nor
	// remove the directory it is in.
	file := execOutFile(t, execSucceed(t, ctrRunCall(t, ctrWriteTool(), ctrPulled())))

	info, err := os.Stat(file.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if info.Size() == 0 {
		t.Errorf("%s is empty, want the container's output", file.Path)
	}

	err = os.Remove(file.Path)
	if err != nil {
		t.Errorf("removing a container-written output: %v", err)
	}
}

func TestDockerOutputDirectoryOverrides(t *testing.T) {
	t.Parallel()

	// dockerOutputDirectory moves where the designated output directory appears inside the
	// container, and nothing else: the tool writes to the path it names, the bytes land in this
	// invocation's own output directory, and the glob finds them there. Conformance test
	// dockeroutputdir is this shape exactly.
	declared := ctrPulled()
	declared.DockerOutputDirectory = ctrOtherDir

	tool := execTool([]string{"touch", ctrOtherDir + "/thing"}, execFileOut("thing"))
	file := execOutFile(t, execSucceed(t, ctrRunCall(t, tool, declared)))

	if filepath.Base(file.Path) != "thing" {
		t.Errorf("path = %q, want the file the tool touched", file.Path)
	}

	if strings.HasPrefix(file.Path, ctrOtherDir) {
		t.Errorf("path = %q, want a path on this host rather than the container's", file.Path)
	}
}

func TestContainerReadsAStagedInput(t *testing.T) {
	t.Parallel()

	// The whole of the two-namespace argument in one run: an input file that lives outside every
	// directory the container has is planned at a path inside one, bind-mounted there, and named
	// in the argv by that path.
	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Stdout = execOutName
	tool.Inputs = []cwlcore.CommandInputParameter{
		cltInput(execInPort, cltFile, cltAt(1)),
	}

	call := ctrRunCall(t, tool, ctrPulled())
	call.Inputs = map[string]any{execInPort: execSourceFile(t, execGreeting)}

	execWantContent(t, execSucceed(t, call), execGreeting)
}

func TestContainerRedirectsStandardInput(t *testing.T) {
	t.Parallel()

	// The redirections need no container-specific work: RunProcess opens them here and hands the
	// *os.File values to the engine's client, which passes the container's own streams straight
	// through. `stdin: $(inputs.f.path)` is the case that proves the mapping back, the path in it
	// being one only the tool has.
	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Stdin = "$(inputs." + execInPort + ".path)"
	tool.Stdout = execOutName

	call := ctrRunCall(t, tool, ctrPulled())
	call.Inputs = map[string]any{execInPort: execSourceFile(t, execGreeting)}

	execWantContent(t, execSucceed(t, call), execGreeting)
}

func TestContainerStagesAnAbsoluteEntryname(t *testing.T) {
	t.Parallel()

	// Legal only under a DockerRequirement in requirements, and the reason the specification
	// gives is the one that makes it work: inside a container "the root filesystem is not shared
	// with any other user or running program", so a mount at /etc/tool.conf disturbs nothing.
	staged := cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
		Entryname: "/etc/tool.conf",
		Entry:     cwlcore.Expression(execGreeting),
	})

	tool := execScript("cat /etc/tool.conf > "+execOutName, execFileOut(execOutName))

	call := ctrRunCall(t, tool, ctrPulled(),
		&cwlcore.InitialWorkDirRequirement{Listing: stgEntries(staged)})

	execWantContent(t, execSucceed(t, call), execGreeting)
}

func TestContainerInvocationHandsOutputCollectionHostPaths(t *testing.T) {
	t.Parallel()

	// Needs an engine, to resolve the image, but never starts a container: what it asserts is the
	// pair of views one invocation keeps. Every stage that describes what the tool will do reads
	// the tool's, and output collection — the one that goes back to a real filesystem — reads
	// this host's, at the durable path rather than at the placement; see
	// [TestContainerOutputsNameInputsWhereTheyOutliveTheStep].
	source := execSourceFile(t, execGreeting)

	call := ctrRunCall(t, execTool([]string{execTrue}), ctrPulled())
	call.Inputs = map[string]any{execInPort: source}

	run := runInvocation(t, call)

	err := run.prepare(t.Context())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	pmWantPath(t, run.inputs[execInPort], containerStagedir+"/"+execSourceName)
	pmWantPath(t, run.hostInputs()[execInPort], pathOf(source))

	// runtime.outdir and runtime.tmpdir are the tool's, because the document's own expressions
	// are what read them; the invocation's own directories stay this host's.
	if run.runtime.Outdir != run.box.toolOutdir || run.runtime.Tmpdir != containerTmpdir {
		t.Errorf("runtime = %q, %q; want the paths the tool sees", run.runtime.Outdir, run.runtime.Tmpdir)
	}

	if run.outdir != call.OutDir {
		t.Errorf("outdir = %q, want the directory allocated on this host", run.outdir)
	}
}
