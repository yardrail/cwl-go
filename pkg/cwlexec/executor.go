package cwlexec

import (
	"context"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// ContainerExecutor runs a tool inside a software container.
// Set via [Config.ContainerExecutor]; nil means no container support.
type ContainerExecutor interface {
	// EnsureImage makes the required image available (pull, build, etc.).
	EnsureImage(ctx context.Context, req *cwlcore.DockerRequirement) error
	// NewInvocation creates the working environment for one tool execution. Caller must Close.
	NewInvocation(ctx context.Context, ctr *ContainerSpec) (Invocation, error)
}

// Invocation is one tool execution's working environment.
type Invocation interface {
	StageFS() WriteFS
	OutFS() WriteFS
	TmpFS() WriteFS
	// Run executes the tool. A non-zero [ProcessResult.ExitCode] is not an error.
	// On error, Run may still return a non-nil ProcessResult carrying whatever
	// was captured before the failure.
	Run(ctx context.Context, spec *ProcessSpec) (*ProcessResult, error)
	Close() error
}

// ProcessResult is everything one tool execution produced besides its collected
// outputs. Captured streams are a bounded diagnostic copy for journaling:
// declared stdout/stderr File outputs are written losslessly per
// [ProcessSpec] and are unaffected by CaptureLimit.
type ProcessResult struct {
	ExitCode int
	// Stdout and Stderr are captured prefixes, each at most CaptureLimit bytes.
	Stdout []byte
	Stderr []byte
	// StdoutTruncated and StderrTruncated report whether the corresponding
	// stream exceeded CaptureLimit and was cut off.
	StdoutTruncated bool
	StderrTruncated bool
	// CaptureLimit is the per-stream byte cap the executor applied.
	// Zero means the executor does not capture streams in memory.
	CaptureLimit int
	// TerminationReason and TerminationMessage describe how the containing
	// runtime ended the process (e.g. a Kubernetes containerStatus reason);
	// empty for local execution.
	TerminationReason  string
	TerminationMessage string
}

// ContainerSpec describes one container invocation.
type ContainerSpec struct {
	Image string
	// Mounts are bind mounts. The runtime directories (outdir, tmpdir,
	// staging) are identified by [Mount.Role], not by position.
	Mounts        []Mount
	WorkDir       string
	NetworkAccess bool
	ReadOnlyRoot  bool
	MatchUser     bool
	Remove        bool
}

// OutDir returns the host source of the CWL runtime output directory mount,
// or "" if absent.
func (c *ContainerSpec) OutDir() string { return c.roleSource(MountRoleOut) }

// TmpDir returns the host source of the CWL runtime temporary directory
// mount, or "" if absent.
func (c *ContainerSpec) TmpDir() string { return c.roleSource(MountRoleTmp) }

// StagingDir returns the host source of the input staging directory mount,
// or "" if absent.
func (c *ContainerSpec) StagingDir() string { return c.roleSource(MountRoleStage) }

// MountByRole returns the first mount tagged with role, and whether one
// exists. Use it when the container-side Target (or ReadOnly flag) is needed
// rather than the host Source.
func (c *ContainerSpec) MountByRole(role MountRole) (Mount, bool) {
	for i := range c.Mounts {
		if c.Mounts[i].Role == role {
			return c.Mounts[i], true
		}
	}

	return Mount{}, false //nolint:exhaustruct_v5 // zero Mount signals absence
}

func (c *ContainerSpec) roleSource(role MountRole) string {
	m, _ := c.MountByRole(role)

	return m.Source
}

// MountRole identifies what a mount provides to the tool container.
type MountRole int

const (
	// MountRoleData is a per-placement input or output file/directory mount.
	MountRoleData MountRole = iota
	// MountRoleOut is the CWL runtime output directory (runtime.outdir).
	MountRoleOut
	// MountRoleTmp is the CWL runtime temporary directory (runtime.tmpdir).
	MountRoleTmp
	// MountRoleStage is the input staging directory.
	MountRoleStage
)

// Mount is one bind mount from host to container.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
	Role     MountRole
}
