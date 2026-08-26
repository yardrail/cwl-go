package cwlexec

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The fixture spellings the container tables repeat. Nothing here touches a daemon or a disk: the
// point of every test in this file is that assembling the invocation is a pure function of the
// requirement, the plan and the spec.
const (
	// ctrImage is a small, portable image. The argv tables never run it; the end-to-end tests do.
	ctrImage = "alpine"

	// ctrHostOut and ctrHostTmp stand in for the two directories an invocation allocates, and
	// ctrToolOut for where the first of them appears inside the container.
	ctrHostOut = "/host/out"
	ctrHostTmp = "/host/tmp"
	ctrToolOut = "/tool/out"

	// ctrHostStage is where newContainer puts the staging directory, under the scratch one.
	ctrHostStage = ctrHostTmp + "/" + containerStageName

	// ctrSource is a file on the host that a placement stages from.
	ctrSource = "/data/reads.bam"

	// ctrOtherDir is the output directory a dockerOutputDirectory row moves the tool's to.
	ctrOtherDir = "/other"

	// ctrWorkdirArg, ctrRmArg and ctrReadOnlyArg are argv elements several rows look for by name.
	ctrWorkdirArg  = "--workdir=" + ctrToolOut
	ctrRmArg       = "--rm"
	ctrReadOnlyArg = "--read-only=true"
)

// ctrBox returns a container over the fixture directories, with a fixed output directory so that
// the argv it assembles is written out rather than described, and asked for nothing in particular.
func ctrBox() *container {
	return ctrBoxUnder(ContainerPolicy{})
}

// ctrBoxUnder returns the same container under a caller's container policy.
func ctrBoxUnder(policy ContainerPolicy) *container {
	return newContainer(&cwlcore.DockerRequirement{
		DockerPull:            ctrImage,
		DockerOutputDirectory: ctrToolOut,
	}, ctrHostOut, ctrHostTmp, policy)
}

// ctrSpec returns the resolved host spec the container wraps: a two-element argv and one variable.
func ctrSpec() *ProcessSpec {
	return &ProcessSpec{
		Command: &CommandLine{Args: plainArgs([]string{execEcho, execGreeting})},
		Dir:     ctrHostOut,
		Env:     []string{envHome + "=" + ctrToolOut},
	}
}

// ctrArgv wraps a spec and returns the argument vector it would be run as.
func ctrArgv(box *container, spec *ProcessSpec, network bool) []string {
	return box.wrap(spec, nil, network).argv()
}

// ctrIndex returns the position of an argv element, or -1.
func ctrIndex(argv []string, want string) int {
	return slices.Index(argv, want)
}

func TestContainerArgvShape(t *testing.T) {
	t.Parallel()

	argv := ctrArgv(ctrBox(), ctrSpec(), false)

	cases := []struct {
		name string
		ok   func([]string) bool
	}{{
		name: "the engine, its subcommand and an attached stdin come first",
		// -i is what keeps a redirected standard input connected; cwltool passes it for the
		// same reason. Conformance test stdin_shorcut is the one that needs it.
		ok: func(a []string) bool { return slices.Equal(a[:3], []string{containerEngine, containerRun, "-i"}) },
	}, {
		name: "the working directory is the one the tool sees",
		ok:   func(a []string) bool { return ctrIndex(a, ctrWorkdirArg) > 0 },
	}, {
		name: "every mount precedes the working directory",
		ok: func(a []string) bool {
			return slices.IndexFunc(a, ctrIsMount) < ctrIndex(a, ctrWorkdirArg)
		},
	}, {
		name: "the container is removed when it exits",
		ok:   func(a []string) bool { return ctrIndex(a, ctrRmArg) > 0 },
	}, {
		name: "the root filesystem is read-only, so only the mounts are writable",
		ok:   func(a []string) bool { return ctrIndex(a, ctrReadOnlyArg) > 0 },
	}, {
		name: "the environment reaches the tool as --env",
		ok:   func(a []string) bool { return ctrIndex(a, "--env="+envHome+"="+ctrToolOut) > 0 },
	}, {
		name: "the image precedes the tool argv and every option precedes the image",
		ok: func(a []string) bool {
			image := ctrIndex(a, ctrImage)

			return image > ctrIndex(a, ctrRmArg) && slices.Equal(a[image+1:], []string{execEcho, execGreeting})
		},
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if !testCase.ok(argv) {
				t.Errorf("argv = %q", argv)
			}
		})
	}
}

// ctrIsMount reports whether an argv element is a bind mount.
func ctrIsMount(arg string) bool {
	return strings.HasPrefix(arg, "--mount=")
}

func TestContainerArgvCarriesTheShellItself(t *testing.T) {
	t.Parallel()

	// A ShellCommandRequirement puts /bin/sh at the front of the tool's own argv, and that shell
	// has to be the *container's*. Wrapping the rendered argv rather than CommandLine.Argv is
	// what puts it there; conformance test filesarray_secondaryfiles is one that needs it.
	spec := ctrSpec()
	spec.Command = &CommandLine{Args: plainArgs([]string{execEcho, cltHello}), Shell: true}

	argv := ctrArgv(ctrBox(), spec, false)
	image := ctrIndex(argv, ctrImage)

	if !slices.Equal(argv[image+1:], []string{shellPath, execDashC, execEcho + " " + cltHello}) {
		t.Errorf("tool argv = %q, want the shell invocation", argv[image+1:])
	}
}

func TestContainerMountsPairHostAndToolPaths(t *testing.T) {
	t.Parallel()

	box := ctrBox()
	mapper := box.mapper()

	err := mapper.Stage(pmHostFile(ctrSource), "reads.bam", false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	_, err = mapper.StageContents("note.txt", execGreeting)
	if err != nil {
		t.Fatalf("StageContents: %v", err)
	}

	want := []string{
		// The three directories mounted whole come first, so that a placement's own mount
		// is applied over the one it falls inside rather than under it.
		"--mount=type=bind,source=" + ctrHostOut + ",target=" + ctrToolOut,
		"--mount=type=bind,source=" + ctrHostTmp + ",target=" + containerTmpdir,
		"--mount=type=bind,source=" + ctrHostStage + ",target=" + containerStagedir,
		// The staged file keeps its bytes where they are, so it needs one of its own.
		"--mount=type=bind,source=" + ctrSource + ",target=" + ctrToolOut + "/reads.bam,readonly",
		// The literal was written inside the output directory, which is already mounted.
	}

	if got := box.mounts(mapper.Plan()); !slices.Equal(got, want) {
		t.Errorf("mounts = %q, want %q", got, want)
	}
}

func TestContainerMountsAWritableLinkReadWrite(t *testing.T) {
	t.Parallel()

	// An InplaceUpdateRequirement turns a writable entry into a link to the original, and the
	// whole point of that is for the tool's writes to reach the original. A read-only mount
	// would make the link resolve and the write fail.
	box := ctrBox()
	mapper := box.mapper()
	mapper.AllowInplaceUpdate()
	mapper.AllowAbsoluteTargets()

	err := mapper.Stage(pmHostFile(ctrSource), "/elsewhere/reads.bam", true)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	want := "--mount=type=bind,source=" + ctrSource + ",target=/elsewhere/reads.bam"
	if got := box.mounts(mapper.Plan()); !slices.Contains(got, want) {
		t.Errorf("mounts = %q, want one reading %q", got, want)
	}
}

func TestContainerMountsAnOutsideLiteral(t *testing.T) {
	t.Parallel()

	// A literal written under an absolute entryname is materialized on this host under the
	// staging directory, because a bind mount needs a source that exists; the mount is then the
	// only route the tool has to it.
	box := ctrBox()
	mapper := box.mapper()
	mapper.AllowAbsoluteTargets()

	_, err := mapper.StageContents("/etc/tool.conf", execGreeting)
	if err != nil {
		t.Fatalf("StageContents: %v", err)
	}

	want := "--mount=type=bind,source=" + ctrHostStage + "/" + outsideName +
		"/etc/tool.conf,target=/etc/tool.conf,readonly"

	if got := box.mounts(mapper.Plan()); !slices.Contains(got, want) {
		t.Errorf("mounts = %q, want one reading %q", got, want)
	}
}

func TestContainerMountQuotesASeparatorInAPath(t *testing.T) {
	t.Parallel()

	// The options are a CSV record. A path holding a comma would otherwise be read as two
	// options, and a mount would be built from something the document never named.
	got := mountArg(`/data/a,b`, `/tool/"q"`, mountModes[false]...)

	want := `--mount=type=bind,"source=/data/a,b","target=/tool/""q""",readonly`
	if got != want {
		t.Errorf("mountArg = %q, want %q", got, want)
	}
}

func TestContainerUserDefaultsToInvoker(t *testing.T) {
	t.Parallel()

	// Without it the tool runs as the image's user, normally root, and every file it leaves in
	// the mounted output directory is owned by root on this host — unreadable and undeletable by
	// the process that has to collect it.
	box := ctrBox()

	want := fmt.Sprintf("--user=%d:%d", os.Geteuid(), os.Getgid())
	if got := ctrArgv(box, ctrSpec(), false); !slices.Contains(got, want) {
		t.Errorf("argv = %q, want one element %q", got, want)
	}

	box = ctrBoxUnder(ContainerPolicy{NoMatchUser: true})

	if got := ctrArgv(box, ctrSpec(), false); slices.ContainsFunc(got, ctrIsUser) {
		t.Errorf("argv = %q, want no --user under the opt-out", got)
	}
}

func TestContainerPolicyOptOutsReachTheArgv(t *testing.T) {
	t.Parallel()

	// Each of cwltool's three argv-level opt-outs removes exactly the argument it names and
	// leaves the rest of the invocation alone. The zero policy is asserted alongside, because
	// "the flag works" and "the default is unchanged" are the same table.
	cases := []struct {
		name   string
		arg    string
		policy ContainerPolicy
		want   bool
	}{{
		name:   "the root filesystem is read-only by default",
		policy: ContainerPolicy{},
		arg:    ctrReadOnlyArg,
		want:   true,
	}, {
		name:   "--no-read-only leaves it writable",
		policy: ContainerPolicy{NoReadOnly: true},
		arg:    ctrReadOnlyArg,
		want:   false,
	}, {
		name:   "the container is removed by default",
		policy: ContainerPolicy{},
		arg:    ctrRmArg,
		want:   true,
	}, {
		name:   "--leave-container keeps it",
		policy: ContainerPolicy{Keep: true},
		arg:    ctrRmArg,
		want:   false,
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			argv := ctrArgv(ctrBoxUnder(testCase.policy), ctrSpec(), false)
			if slices.Contains(argv, testCase.arg) != testCase.want {
				t.Errorf("argv = %q; %q present = %v, want %v",
					argv, testCase.arg, !testCase.want, testCase.want)
			}

			// Nothing else moves: the image and the tool's own argv still close the vector.
			image := ctrIndex(argv, ctrImage)
			if !slices.Equal(argv[image+1:], []string{execEcho, execGreeting}) {
				t.Errorf("tool argv = %q, want it left alone", argv[image+1:])
			}
		})
	}
}

// ctrIsUser reports whether an argv element selects the container's user.
func ctrIsUser(arg string) bool {
	return strings.HasPrefix(arg, "--user=")
}

func TestContainerNetworkIsOffByDefault(t *testing.T) {
	t.Parallel()

	// NetworkAccess: "If networkAccess is false or not specified, tools must not assume network
	// access". Conformance test networkaccess_disabled is a should_fail test whose tool declares
	// nothing and opens a connection, so the default has to be enforced rather than assumed.
	if got := ctrArgv(ctrBox(), ctrSpec(), false); !slices.Contains(got, "--net=none") {
		t.Errorf("argv = %q, want --net=none with no NetworkAccess requirement", got)
	}

	if got := ctrArgv(ctrBox(), ctrSpec(), true); slices.Contains(got, "--net=none") {
		t.Errorf("argv = %q, want the network left alone when NetworkAccess grants it", got)
	}
}

func TestContainerSilencesTheEngineLogWhenStdoutIsCaptured(t *testing.T) {
	t.Parallel()

	if got := ctrArgv(ctrBox(), ctrSpec(), false); slices.Contains(got, "--log-driver=none") {
		t.Errorf("argv = %q, want no log driver argument with nothing captured", got)
	}

	spec := ctrSpec()
	spec.Stdout = ctrHostOut + "/" + execOutName

	if got := ctrArgv(ctrBox(), spec, false); !slices.Contains(got, "--log-driver=none") {
		t.Errorf("argv = %q, want the engine's own log turned off", got)
	}
}

func TestContainerClientKeepsThisProcessEnvironment(t *testing.T) {
	t.Parallel()

	// The wrapped spec's Env belongs to the engine's *client*, which needs this machine's own —
	// a DOCKER_HOST, a certificate directory, a PATH to find nothing else by. The tool's
	// environment travels as --env arguments instead.
	wrapped := ctrBox().wrap(ctrSpec(), nil, false)

	if !slices.Equal(wrapped.Env, os.Environ()) {
		t.Errorf("Env = %q, want this process's own", wrapped.Env)
	}
}

func TestContainerDropsTheInheritedPath(t *testing.T) {
	t.Parallel()

	// PATH is the one variable ToolEnvironment fills in from this process. Inside a container
	// the image's own is the right one, and overriding it hides the program the tool names.
	env := []string{envHome + "=/h", envPath + "=/host/bin", envTmpDir + "=/t"}

	got := withoutInheritedPath(env, nil)
	if !slices.Equal(got, []string{envHome + "=/h", envTmpDir + "=/t"}) {
		t.Errorf("env = %q, want the inherited PATH gone", got)
	}

	// One the document wrote is the document's, not this process's, and stays.
	declared := execScope(&cwlcore.EnvVarRequirement{
		EnvDef: []cwlcore.EnvironmentDef{{EnvName: envPath, EnvValue: "/opt/bin"}},
	})

	if got := withoutInheritedPath(env, declared); !slices.Equal(got, env) {
		t.Errorf("env = %q, want an EnvVarRequirement's PATH kept", got)
	}
}

func TestContainerOutdirIsRandomUnlessDeclared(t *testing.T) {
	t.Parallel()

	// cwltool draws a random directory rather than using a fixed one, and warns about documents
	// that name the historical /var/spool/cwl: a document that hardcodes its output directory is
	// not portable, and a random one is what makes that fail here rather than elsewhere.
	drawn := outdirFor(&cwlcore.DockerRequirement{DockerPull: ctrImage})

	if len(drawn) != containerOutdirLetters+1 || !strings.HasPrefix(drawn, "/") {
		t.Errorf("outdir = %q, want a %d-character name under the root", drawn, containerOutdirLetters)
	}

	// One per process, so that two steps of a workflow agree about the path.
	if again := outdirFor(&cwlcore.DockerRequirement{DockerPull: ctrImage}); again != drawn {
		t.Errorf("outdir = %q then %q, want one for the life of the process", drawn, again)
	}

	cases := map[string]string{
		"an absolute path is taken as written": ctrOtherDir,
		"and cleaned":                          ctrOtherDir + "/",
	}

	for name, declared := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := outdirFor(&cwlcore.DockerRequirement{DockerOutputDirectory: declared})
			if got != ctrOtherDir {
				t.Errorf("outdir = %q, want %q", got, ctrOtherDir)
			}
		})
	}

	// A relative one names nothing a mount can be built from.
	if got := outdirFor(&cwlcore.DockerRequirement{DockerOutputDirectory: "out"}); got != drawn {
		t.Errorf("outdir = %q, want the default for a relative dockerOutputDirectory", got)
	}
}

func TestContainerNetworkAccessIsResolvedFromTheScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope *cwlcore.RequirementScope
		name  string
		want  bool
	}{{
		name:  "nothing declared",
		scope: nil,
		want:  false,
	}, {
		name:  "a scope that declares something else",
		scope: execScope(&cwlcore.WorkReuse{EnableReuse: cwlcore.NewExprBool(false)}),
		want:  false,
	}, {
		name:  "declared and left unset",
		scope: execScope(&cwlcore.NetworkAccess{}),
		want:  false,
	}, {
		name:  "declared false",
		scope: execScope(&cwlcore.NetworkAccess{NetworkAccess: cwlcore.NewExprBool(false)}),
		want:  false,
	}, {
		name:  "declared true",
		scope: execScope(&cwlcore.NetworkAccess{NetworkAccess: cwlcore.NewExprBool(true)}),
		want:  true,
	}, {
		// A hint counts: the field states what the tool needs, and an engine that can grant
		// it has no reason to withhold it for the statement having been written advisorily.
		name:  "hinted true",
		scope: execHintScope(&cwlcore.NetworkAccess{NetworkAccess: cwlcore.NewExprBool(true)}),
		want:  true,
	}, {
		name: "written as an expression over the inputs",
		scope: execScope(&cwlcore.NetworkAccess{
			NetworkAccess: cwlcore.NewExprBoolExpression("$(inputs." + execInPort + ")"),
		}),
		want: true,
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := ToolNetworkAccess(testCase.scope, map[string]any{execInPort: true},
				cltEval(), cwlcore.RuntimeContext{})
			if err != nil {
				t.Fatalf("ToolNetworkAccess: %v", err)
			}

			if got != testCase.want {
				t.Errorf("ToolNetworkAccess = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestContainerNetworkAccessReportsABadExpression(t *testing.T) {
	t.Parallel()

	scope := execScope(&cwlcore.NetworkAccess{
		NetworkAccess: cwlcore.NewExprBoolExpression(execMissingRef),
	})

	_, err := ToolNetworkAccess(scope, nil, cltEval(), cwlcore.RuntimeContext{})
	if !errors.Is(err, cwlcore.ErrExpressionEval) {
		t.Errorf("ToolNetworkAccess: error %v does not wrap %v", err, cwlcore.ErrExpressionEval)
	}
}
