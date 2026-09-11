package cwlexec

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Container execution: resolves DockerRequirement into a [ContainerSpec] and assembles mounts.

// Fixed container paths, following cwltool. See [containerOutdir] for the output directory.
const (
	// containerStagedir is the container-side staging directory for input files.
	containerStagedir = "/var/lib/cwl"

	// containerTmpdir is the container's $TMPDIR / runtime.tmpdir.
	containerTmpdir = "/tmp"
)

// containerOutdirLetters is the number of random characters in the container output directory name.
const containerOutdirLetters = 6

// containerOutdir is the container's output directory path.
// Random so documents can't hardcode it; memoized so all steps agree.
var containerOutdir = sync.OnceValue(randomOutdir)

// randomOutdir generates the process-wide container output directory name.
func randomOutdir() string {
	return "/" + rand.Text()[:containerOutdirLetters]
}

// ContainerPolicy controls container opt-outs. Zero value runs containers normally.
// Fields mirror cwltool's flags.
type ContainerPolicy struct {
	// Disabled skips container execution (cwltool's --no-container).
	Disabled bool

	// NoMatchUser runs as the image's user instead of the host user (--no-match-user).
	NoMatchUser bool

	// NoReadOnly leaves the container's root filesystem writable (--no-read-only).
	NoReadOnly bool

	// Keep leaves the container after exit for inspection (--leave-container).
	Keep bool
}

// container is the resolved container configuration for one invocation.
type container struct {
	image      string // image reference; see [imageReference]
	hostOutdir string // host output directory
	hostStage  string // host staging directory
	hostTmpdir string // host scratch directory
	toolOutdir string // output directory path inside the container
	policy     ContainerPolicy
}

// newContainer resolves a DockerRequirement into invocation config. No I/O; image is named, not fetched.
func newContainer(
	declared *cwlcore.DockerRequirement, hostOutdir, hostTmpdir string, policy ContainerPolicy,
) *container {
	return &container{
		image:      imageReference(declared),
		hostOutdir: hostOutdir,
		hostStage:  filepath.Join(hostTmpdir, containerStageName),
		hostTmpdir: hostTmpdir,
		toolOutdir: outdirFor(declared),
		policy:     policy,
	}
}

// containerStageName is the subdirectory under tmpdir where container staging files are placed.
const containerStageName = "stg"

// outdirFor returns dockerOutputDirectory if absolute, otherwise the random default.
func outdirFor(declared *cwlcore.DockerRequirement) string {
	if filepath.IsAbs(declared.DockerOutputDirectory) {
		return filepath.Clean(declared.DockerOutputDirectory)
	}

	return containerOutdir()
}

// mapper returns a [PathMap] that plans container-side paths for this invocation.
func (c *container) mapper() *PathMap {
	return NewContainerPathMap(c.hostOutdir, c.hostStage, c.toolOutdir, containerStagedir)
}

// dirs creates host directories that will be mounted into the container.
func (c *container) dirs() error {
	return os.MkdirAll(c.hostStage, stageDirPerm)
}

// containerSpec builds the [ContainerSpec] from resolved config and planned placements.
func (c *container) containerSpec(plan []PathMapping, network bool) *ContainerSpec {
	return &ContainerSpec{
		Image:         c.image,
		Mounts:        c.containerMounts(plan),
		WorkDir:       c.toolOutdir,
		NetworkAccess: network,
		ReadOnlyRoot:  !c.policy.NoReadOnly,
		MatchUser:     !c.policy.NoMatchUser,
		Remove:        !c.policy.Keep,
		Stdout:        "",
	}
}

// containerMounts builds the mount list: three whole-directory mounts, then per-placement mounts.
func (c *container) containerMounts(plan []PathMapping) []Mount {
	mounts := make([]Mount, 0, len(plan)+containerWholeMounts)

	mounts = append(mounts,
		Mount{Source: c.hostOutdir, Target: c.toolOutdir, ReadOnly: false},
		Mount{Source: c.hostTmpdir, Target: containerTmpdir, ReadOnly: false},
		Mount{Source: c.hostStage, Target: containerStagedir, ReadOnly: false})

	for index := range plan {
		mapping := &plan[index]

		source, needed := c.mountSource(mapping)
		if !needed {
			continue
		}

		mounts = append(mounts, Mount{
			Source: source, Target: mapping.Target, ReadOnly: !mapping.Writable,
		})
	}

	return mounts
}

// containerWholeMounts is the count of whole-directory mounts (outdir, stage, tmpdir).
const containerWholeMounts = 3

// mountSource returns the host path to bind-mount for a placement, and whether a mount is needed.
// Links always need a mount; materialized files only need one if outside the whole-directory mounts.
func (c *container) mountSource(mapping *PathMapping) (string, bool) {
	if mapping.Action == StageLink {
		return mapping.Resolved, true
	}

	if c.encloses(mapping.Target) {
		return "", false
	}

	return mapping.Host, true
}

// encloses reports whether target falls inside one of the whole-directory mounts.
func (c *container) encloses(target string) bool {
	for _, dir := range []string{c.toolOutdir, containerStagedir, containerTmpdir} {
		if _, ok := relativeTo(dir, target); ok {
			return true
		}
	}

	return false
}
