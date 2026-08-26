package cwlexec

import (
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// What a caller's [ContainerPolicy] does to one CommandLineTool invocation.
//
// Nothing in this file starts, pulls or inspects an image, and that is load-bearing rather than
// incidental: the reason --no-container exists is that a machine's container engine may not work,
// and a test of it that needed one would never run where it matters.

func TestNoContainerDeclinesAHintAndRunsOnThisHost(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	// The reason the opt-out exists, and the one container-policy test that has to pass on a
	// machine where containers do not work at all: nothing here starts, pulls or inspects an
	// image. "Hints are advisory: an implementation may ignore a hint" is exactly the licence a
	// caller is exercising, so the tool runs here and produces the output it would have produced
	// inside the image.
	call := execCall(t, execScript(execWriteScript, execFileOut(execOutName)))
	call.Requirements = execHintScope(&cwlcore.DockerRequirement{DockerPull: ctrImage})
	call.Containers = ContainerPolicy{Disabled: true}

	execWantContent(t, execSucceed(t, call), execGreeting)

	run := runInvocation(t, call)
	if run.box != nil || run.docker != nil {
		t.Errorf("container = %+v, want none resolved at all", run.box)
	}

	// runtime.outdir stays this host's, which is what an argv or a glob written over it means.
	if run.runtime.Outdir != call.OutDir || run.runtime.Tmpdir != call.TmpDir {
		t.Errorf("runtime = %q, %q; want the directories allocated on this host",
			run.runtime.Outdir, run.runtime.Tmpdir)
	}
}

func TestADockerRequirementHintStillRunsAContainerByDefault(t *testing.T) {
	t.Parallel()

	// The other half of the same statement: without the opt-out nothing changes, and a hint is
	// honoured. Resolving the container is enough to say so — newInvocation names an image
	// rather than fetching one — so this needs no engine either.
	call := execCall(t, execTool([]string{execTrue}))
	call.Requirements = execHintScope(&cwlcore.DockerRequirement{DockerPull: ctrImage})

	run := runInvocation(t, call)
	if run.box == nil {
		t.Fatal("container = nil, want a hint honoured under the zero policy")
	}

	if run.box.image != ctrImage {
		t.Errorf("image = %q, want %q", run.box.image, ctrImage)
	}

	// An absolute entryname stays illegal: that turns on where the declaration was written,
	// which the opt-out does not change.
	if run.absolute {
		t.Error("absolute targets are allowed, want them refused under a hint")
	}
}

func TestNoContainerRefusesADockerRequirementUnderRequirements(t *testing.T) {
	t.Parallel()

	// cwltool's make_job_runner raises UnsupportedRequirement here — "--no-container, but this
	// CommandLineTool has DockerRequirement under 'requirements'" — and that is its exit status
	// 33. A requirement is not advisory: the document says the tool must run in that image, so
	// running it on this host would be a different answer rather than a lesser one.
	call := execCall(t, execTool([]string{execTrue}))
	call.Requirements = execScope(&cwlcore.DockerRequirement{DockerPull: ctrImage})
	call.Containers = ContainerPolicy{Disabled: true}

	if got := execFail(t, call, ErrUnsupportedFeature); got != StatusPermanentFail {
		t.Errorf("status = %q, want a permanent failure", got)
	}
}

func TestContainerPolicyReachesTheContainer(t *testing.T) {
	t.Parallel()

	// The three argv-level opt-outs travel from the call to the container the invocation
	// resolves, which is where [container.wrap] reads them.
	policy := ContainerPolicy{NoMatchUser: true, NoReadOnly: true, Keep: true}

	call := execCall(t, execTool([]string{execTrue}))
	call.Requirements = execScope(&cwlcore.DockerRequirement{DockerPull: ctrImage})
	call.Containers = policy

	run := runInvocation(t, call)
	if run.box == nil {
		t.Fatal("container = nil, want one resolved")
	}

	if run.box.policy != policy {
		t.Errorf("policy = %+v, want %+v", run.box.policy, policy)
	}
}

func TestContainerPolicyDescendsIntoANestedRun(t *testing.T) {
	t.Parallel()

	// A nested run is configured by childConfig, and the policy has to come off the invocation's
	// own StepCall rather than only out of the context: a caller who never called
	// WithSubworkflows would otherwise get a subworkflow that started the containers it had
	// forbidden one level up, which is worse than not having the setting at all.
	policy := ContainerPolicy{Disabled: true, NoMatchUser: true}

	call := &StepCall{StepID: stepID, OutDir: "/out", TmpDir: "/tmp", Containers: policy}

	if got := (subworkflowEnv{}).childConfig(call).Containers; got != policy {
		t.Errorf("nested Containers = %+v, want %+v", got, policy)
	}
}
