package cwlexec

import (
	"context"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The pluggable container-execution contract.
//
// A CWL engine needs two things from a container runtime: the ability to make an image available
// (pull, build, import) and the ability to run a process inside it. Everything about what to mount,
// where to work, and what environment the tool sees is already resolved by this package into a
// [ContainerSpec]; the executor decides *how* to carry that out.
//
// The default implementation is [DockerCLIExecutor], which shells out to the `docker` binary. A
// consumer that needs a different runtime — the Docker SDK, a Kubernetes Job, a remote execution
// service — injects its own through [Config.ContainerExecutor].

// ContainerExecutor runs a tool inside a software container. The consumer injects an implementation
// via [Config.ContainerExecutor]; nil falls back to [DockerCLIExecutor], preserving the existing
// subprocess behaviour.
type ContainerExecutor interface {
	// EnsureImage makes the image a [cwlcore.DockerRequirement] names available for execution.
	// It is called once per image per run, before the first invocation that needs it.
	EnsureImage(ctx context.Context, req *cwlcore.DockerRequirement) error

	// Run executes a tool inside a container described by ctr and returns its exit code.
	//
	// spec is the unwrapped tool command: the argv the tool itself sees, the environment the CWL
	// specification prescribes, and the stream redirections resolved onto this host. The executor
	// is responsible for connecting those streams — spec.Stdin, spec.Stdout, spec.Stderr name
	// files on this host that must be opened and wired to the containerised process.
	//
	// A non-zero exit code is not an error; exit-code classification is the caller's concern. An
	// error is returned only when the container could not be created, started or waited for.
	Run(ctx context.Context, ctr *ContainerSpec, spec *ProcessSpec) (int, error)
}

// ContainerSpec is the structured description of one container invocation: everything a
// [ContainerExecutor] needs to start a container, and nothing about how any particular runtime
// carries it out.
//
// It replaces the Docker CLI argv that [DockerCLIExecutor] (and the old container.wrap) builds: the
// same information, as a value rather than as strings, so that a Kubernetes executor reads mounts
// and an SDK executor reads mounts and neither of them parses `--mount=type=bind,...`.
type ContainerSpec struct {
	// Image is the resolved image reference the tool runs from.
	Image string

	// Mounts is the set of bind mounts the container needs: the three whole-directory mounts
	// (output, tmp, staging) plus per-file mounts for staged inputs. Order matters: the
	// whole-directory mounts come first so that a per-file mount is applied over the directory it
	// falls inside rather than under it.
	Mounts []Mount

	// WorkDir is the working directory inside the container — the tool's designated output
	// directory as the tool sees it.
	WorkDir string

	// NetworkAccess says whether outgoing network access is granted. False means the executor
	// should isolate the container from the network (Docker's --net=none; Kubernetes's
	// NetworkPolicy; etc.).
	NetworkAccess bool

	// ReadOnlyRoot says whether the container's root filesystem should be mounted read-only, so
	// that the only writable paths are the mounted directories.
	ReadOnlyRoot bool

	// MatchUser says whether the tool should run as this process's uid:gid rather than as the
	// image's own user.
	MatchUser bool

	// Remove says whether the container should be removed when the tool exits.
	Remove bool

	// Stdout is the host path the tool's standard output is captured to, or "" when nothing
	// captures it. An executor may use this as a hint to suppress its own log capture (Docker's
	// --log-driver=none) when the tool's output is large.
	Stdout string
}

// Mount is one bind mount from the host into the container.
type Mount struct {
	// Source is the absolute path on the host.
	Source string

	// Target is the absolute path inside the container.
	Target string

	// ReadOnly says whether the mount is read-only inside the container.
	ReadOnly bool
}
