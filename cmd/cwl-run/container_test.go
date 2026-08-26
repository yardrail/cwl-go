package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlexec"
)

// The container opt-out flags. Nothing in this file needs a container engine — that is the whole
// claim being tested: -no-container is what makes a document carrying a DockerRequirement hint
// runnable on a machine where containers do not work, and a test for it that needed a daemon would
// be testing the opposite thing.

// dockerHintWorkflow nests docker_hint.cwl two levels down: a workflow whose step runs a workflow
// whose step runs the tool. It is what a setting has to survive to be worth having.
const dockerHintWorkflow = "subworkflow_docker.cwl"

// hintedGreeting is what testdata/docker_hint.cwl writes, whichever filesystem it writes it on.
const hintedGreeting = "hinted\n"

// The four flags under test, spelled as cwltool spells them.
const (
	noContainerFlag    = "--no-container"
	noMatchUserFlag    = "--no-match-user"
	noReadOnlyFlag     = "--no-read-only"
	leaveContainerFlag = "--leave-container"
)

// outputFile requires an output to be a File object and returns its path.
func outputFile(t *testing.T, outputs map[string]any, port string) string {
	t.Helper()

	file, ok := outputs[port].(map[string]any)
	if !ok {
		t.Fatalf("output %q = %v, want a File object", port, outputs[port])
	}

	path, ok := file["path"].(string)
	if !ok {
		t.Fatalf("path = %v, want a string", file["path"])
	}

	return path
}

// wantContent requires the file at path to hold want.
func wantContent(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading the collected output: %v", err)
	}

	if string(data) != want {
		t.Errorf("%s holds %q, want %q", path, data, want)
	}
}

func TestContainerFlagsRenderThePolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want cwlexec.ContainerPolicy
	}{{
		// The zero policy is the whole of the no-flags case, and it is what keeps today's
		// behaviour: a DockerRequirement runs a container, as this process's user, on a
		// read-only root filesystem, and the container is removed afterwards.
		name: "no flags ask for nothing",
		args: nil,
		want: cwlexec.ContainerPolicy{},
	}, {
		name: "-no-container, one dash",
		args: []string{"-no-container"},
		want: cwlexec.ContainerPolicy{Disabled: true},
	}, {
		// Go's flag package accepts either spelling, so a script written for cwltool's
		// double dash keeps working when this engine is the cwl-runner on the path.
		name: noContainerFlag,
		args: []string{noContainerFlag},
		want: cwlexec.ContainerPolicy{Disabled: true},
	}, {
		name: noMatchUserFlag,
		args: []string{noMatchUserFlag},
		want: cwlexec.ContainerPolicy{NoMatchUser: true},
	}, {
		name: noReadOnlyFlag,
		args: []string{noReadOnlyFlag},
		want: cwlexec.ContainerPolicy{NoReadOnly: true},
	}, {
		name: leaveContainerFlag,
		args: []string{leaveContainerFlag},
		want: cwlexec.ContainerPolicy{Keep: true},
	}, {
		name: "all four together",
		args: []string{noContainerFlag, noMatchUserFlag, noReadOnlyFlag, leaveContainerFlag},
		want: cwlexec.ContainerPolicy{Disabled: true, NoMatchUser: true, NoReadOnly: true, Keep: true},
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var stderr strings.Builder

			cfg, err := parseFlags(append(testCase.args, fixture(echoTool)), &stderr)
			if err != nil {
				t.Fatalf("parseFlags: %v\n%s", err, stderr.String())
			}

			if got := cfg.containerPolicy(); got != testCase.want {
				t.Errorf("policy = %+v, want %+v", got, testCase.want)
			}

			// The flags are not positional arguments, so the document is still found.
			if cfg.process != fixture(echoTool) {
				t.Errorf("process = %q, want %q", cfg.process, fixture(echoTool))
			}
		})
	}
}

func TestContainerPolicyReachesTheRunConfiguration(t *testing.T) {
	t.Parallel()

	var stderr strings.Builder

	cfg, err := parseFlags([]string{noContainerFlag, fixture(echoTool)}, &stderr)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	settings, err := cfg.execConfig(&stderr)
	if err != nil {
		t.Fatalf("execConfig: %v", err)
	}

	if !settings.Containers.Disabled {
		t.Errorf("Containers = %+v, want the opt-out carried into the run", settings.Containers)
	}
}

func TestRunWithNoContainerRunsADockerHintOnThisHost(t *testing.T) {
	t.Parallel()

	// "Hints are advisory: an implementation may ignore a hint." A caller who has said
	// -no-container is exercising exactly that licence, so the tool runs here and produces the
	// output it would have produced inside the image. No daemon is involved.
	outdir := t.TempDir()

	got := exerciseIn(t, outdir, noContainerFlag, fixture(dockerHint))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	path := outputFile(t, got.outputs(t), "out")
	if !strings.HasPrefix(path, outdir) {
		t.Errorf("path = %q, want it under the -outdir %q", path, outdir)
	}

	wantContent(t, path, hintedGreeting)
}

func TestRunWithNoContainerRefusesAHardDockerRequirement(t *testing.T) {
	t.Parallel()

	// cwltool raises UnsupportedRequirement here — "--no-container, but this CommandLineTool has
	// DockerRequirement under 'requirements'" — and that is its exit 33. The document says the
	// tool must run in that image; running it on this host instead would be a different answer,
	// not a lesser one, so nothing runs and cwltest reads the status as a skip.
	got := exerciseIn(t, t.TempDir(), noContainerFlag, fixture(dockerTool))
	if got.status() != exitUnsupported {
		t.Fatalf("exit status = %d, want %d\n%s", got.status(), exitUnsupported, got.stderr)
	}

	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing when nothing ran", got.stdout)
	}

	if !strings.Contains(got.stderr, "DockerRequirement") {
		t.Errorf("stderr = %q, want it to name the requirement it declined", got.stderr)
	}
}

func TestRunCarriesNoContainerIntoASubworkflow(t *testing.T) {
	t.Parallel()

	// A -no-container that stopped applying one level down would start a container the caller
	// told this engine not to start. The tool here is two levels in — workflow, subworkflow,
	// tool — and it produces its output on this host, which it could only do if the setting
	// travelled the whole way.
	outdir := t.TempDir()

	got := exerciseIn(t, outdir, noContainerFlag, fixture(dockerHintWorkflow))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	path := outputFile(t, got.outputs(t), "result")

	if !strings.Contains(path, filepath.Join("nest", "inner")) {
		t.Errorf("path = %q, want the nesting visible in it", path)
	}

	wantContent(t, path, hintedGreeting)
}
