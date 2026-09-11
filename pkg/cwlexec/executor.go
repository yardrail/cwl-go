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
	// Run executes the tool and returns its exit code. Non-zero is not an error.
	Run(ctx context.Context, spec *ProcessSpec) (int, error)
	Close() error
}

// ContainerSpec describes one container invocation.
type ContainerSpec struct {
	Image string
	// Mounts are bind mounts. Order matters: whole-directory mounts come first.
	Mounts        []Mount
	WorkDir       string
	NetworkAccess bool
	ReadOnlyRoot  bool
	MatchUser     bool
	Remove        bool
	// Stdout is the host capture path, or "" if uncaptured.
	Stdout string
}

// Mount is one bind mount from host to container.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}
