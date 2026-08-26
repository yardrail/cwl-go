package cwlexec

import (
	"os"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The two properties a container invocation has to hold before a container is ever started, each
// standing in for an end-to-end failure that otherwise only shows up on a machine with a working
// engine. Both are cheap, and both fail in exactly the way the daemon-backed rows do.

// TestContainerScriptIsOneShellCommand pins what `sh -c` is handed.
//
// A shell reads its -c argument as a script, not as a command: a newline anywhere in it starts a
// second command. execGreeting ends in one, so interpolating it into a redirection splits
// `echo -n hello\n > out.txt` into `echo -n hello` and a bare `> out.txt` — which succeeds, and
// leaves an empty file. The end-to-end rows then report "out.txt holds \"\"", a full container run
// away from the two-line fixture that caused it.
func TestContainerScriptIsOneShellCommand(t *testing.T) {
	t.Parallel()

	argv := ctrWriteTool().BaseCommand
	if len(argv) != 3 || argv[0] != execShell || argv[1] != execDashC {
		t.Fatalf("BaseCommand = %q, want a %s %s script", argv, execShell, execDashC)
	}

	if strings.ContainsAny(argv[2], "\n\r") {
		t.Errorf("script = %q, want a single command: a newline starts a second one", argv[2])
	}
}

// TestContainerBindMountsNeverLandOnASymlink pins where the engine is told to put a mount.
//
// A bind mount's target is resolved inside the container, and a symbolic link at that path is
// followed rather than mounted over: the engine creates the link's *destination* instead, as root,
// under whichever directory is mounted whole there. A staged input planned at /var/lib/cwl/x that
// this host placed as a link to /tmp/…/x therefore leaves a root-owned /tmp/…/x behind inside the
// host tmpdir mount, which this process can then neither read nor remove — the "permission denied"
// the daemon-backed staging rows fail their own cleanup with.
//
// So a placement the executor bind-mounts must have a real mount point at its host path, of the
// same kind as the bytes being mounted there.
func TestContainerBindMountsNeverLandOnASymlink(t *testing.T) {
	t.Parallel()

	staged := cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
		Entryname: "/etc/tool.conf",
		Entry:     cwlcore.Expression(execGreeting),
	})

	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Inputs = []cwlcore.CommandInputParameter{cltInput(execInPort, cltFile, cltAt(1))}

	call := ctrRunCall(t, tool, ctrPulled(),
		&cwlcore.InitialWorkDirRequirement{Listing: stgEntries(staged)})
	call.Inputs = map[string]any{execInPort: execSourceFile(t, execGreeting)}

	run := runInvocation(t, call)

	err := run.prepare(t.Context())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	plan := run.mapper.Plan()
	if len(plan) == 0 {
		t.Fatal("nothing planned; the fixture no longer stages anything")
	}

	mounted := 0

	for index := range plan {
		mapping := &plan[index]

		_, needed := run.box.mountSource(mapping)
		if !needed || mapping.Host == "" {
			continue
		}

		mounted++

		info, err := os.Lstat(mapping.Host)
		if err != nil {
			t.Errorf("mount point for %q: %v", mapping.Target, err)

			continue
		}

		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("mount point %q for %q is a symbolic link; want a real one, which is "+
				"what the engine mounts over rather than follows", mapping.Host, mapping.Target)
		}
	}

	if mounted == 0 {
		t.Error("no planned placement is bind-mounted; the fixture no longer covers this")
	}
}

// TestContainerStdinOpensTheBytesRatherThanTheMountPoint pins when a redirection is resolved
// relative to when the mount that carries its bytes exists.
//
// `stdin: $(inputs.f.path)` names a path only the tool has, and this process is the one that opens
// it: RunProcess opens the file here and the engine inherits the descriptor. That happens *before*
// the container starts, so the mount point a staged link is placed as is still empty — the bytes
// arrive over it, and are restored to it only once the tool has exited. Opening the placement would
// connect the tool's standard input to nothing, which reads end-to-end as an output file that holds
// "" instead of what the tool was fed.
func TestContainerStdinOpensTheBytesRatherThanTheMountPoint(t *testing.T) {
	t.Parallel()

	tool := execTool([]string{execCat}, execFileOut(execOutName))
	tool.Stdin = "$(inputs." + execInPort + ".path)"
	tool.Stdout = execOutName

	call := ctrRunCall(t, tool, ctrPulled())
	call.Inputs = map[string]any{execInPort: execSourceFile(t, execGreeting)}

	run := runInvocation(t, call)

	err := run.prepare(t.Context())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	spec, err := run.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}

	if spec.Stdin == "" {
		t.Fatal("no standard input resolved; the fixture no longer redirects one")
	}

	if got := execRead(t, spec.Stdin); got != execGreeting {
		t.Errorf("standard input %q holds %q, want %q", spec.Stdin, got, execGreeting)
	}
}
