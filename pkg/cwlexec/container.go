package cwlexec

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Running the tool inside a software container.
//
// A DockerRequirement does not change what a CommandLineTool is asked to do; it changes which
// filesystem and which process namespace the doing happens in. So nothing here rebuilds an argv or
// re-plans a working directory. What it does is resolve the requirement into a [ContainerSpec] — the
// structured description a [ContainerExecutor] consumes — and assemble the mounts that make the
// paths in it mean something on the other side.
//
// The behavioural authority is cwltool's DockerCommandLineJob — create_runtime, add_volumes and
// get_image — because the specification says almost nothing about how a container is to be started
// and the conformance suite was written against that implementation. Deviations are marked where
// they occur.

// The paths a tool sees when it runs inside a container.
//
// Two of the three are fixed, following cwltool: CONTAINER_TMPDIR is "/tmp" (job.py:628) and the
// stage directory for input files is "/var/lib/cwl" (process.py:914). The third — the designated
// output directory — is not fixed; see [containerOutdir].
const (
	// containerStagedir is where a value that only has to be *reachable* appears, which is the
	// container's side of the path map's staging directory.
	//
	// It is deliberately not the temporary directory, even though this engine's host staging
	// directory is: inside a container every input the tool reads is staged, and a tool that
	// also writes scratch files under $TMPDIR would be sharing a directory with them.
	containerStagedir = "/var/lib/cwl"

	// containerTmpdir is the container's $TMPDIR, and what runtime.tmpdir evaluates to.
	containerTmpdir = "/tmp"
)

// containerOutdirLetters is how many random characters [containerOutdir] names a directory with.
// cwltool's random_outdir uses six.
const containerOutdirLetters = 6

// containerOutdir is where a tool's designated output directory appears inside the container, for
// every invocation in this process that does not name one itself.
//
// It is random, which is cwltool's choice and worth restating because a fixed default looks more
// obviously right. cwltool's random_outdir (utils.py:273) builds "/" plus six random letters once
// per process, and process.py:500-516 carries a matching detector that warns about any textual
// reference to /var/spool/cwl — the historical fixed default — telling the author to write
// $(runtime.outdir) or to declare dockerOutputDirectory instead. A document that hardcodes the
// output directory is not portable, and a random directory is what makes that fail immediately
// rather than on somebody else's engine.
//
// It is memoized rather than drawn per invocation for the same reason cwltool memoizes it: a
// workflow's steps then agree about the path, and a log or an error message read across two steps
// says the same thing.
var containerOutdir = sync.OnceValue(randomOutdir)

// randomOutdir draws the process-wide container output directory.
//
// [rand.Text] is the source because it is the only one in the standard library that returns a
// string and no error, so there is no failure path here to invent a fallback for. Its alphabet is
// base32's, which is a subset of what cwltool draws from and needs no escaping in a path.
func randomOutdir() string {
	return "/" + rand.Text()[:containerOutdirLetters]
}

// ContainerPolicy is what a caller has asked this engine to do differently about software
// containers. It reaches an invocation as [Config.Containers] by way of [StepCall.Containers].
//
// Every field is an opt-out, so the zero value is what an engine that was asked nothing does:
// a DockerRequirement runs a container, the tool runs as the user who started this process, the
// container's root filesystem is read-only, and the container is removed when the tool exits.
//
// The four spellings are cwltool's, field for field, because the point of the surface is that a
// script written against `cwltool --no-container` keeps meaning the same thing when this engine is
// the cwl-runner on the path. See cmd/cwl-run for the flags that set them.
type ContainerPolicy struct {
	// Disabled declines every DockerRequirement, which is cwltool's --no-container: "Do not
	// execute jobs in a Docker container, even when DockerRequirement is specified under hints."
	//
	// A hint is declined and the tool runs on this host, which is the liberty
	// "an implementation may ignore a hint" grants. A DockerRequirement under *requirements* is
	// refused instead, with [ErrUnsupportedFeature] — see [invocation.declineContainer].
	Disabled bool

	// NoMatchUser runs the tool as the image's own user rather than as this engine's, which is
	// cwltool's --no-match-user: "Disable passing the current uid to `docker run --user`". See
	// [DockerCLIExecutor] for what that costs.
	NoMatchUser bool

	// NoReadOnly leaves the container's root filesystem writable, which is cwltool's
	// --no-read-only: "Do not set root directory in the container as read-only". A tool that
	// scribbles outside the three mounted directories then works, and stops being a tool this
	// engine can promise anything about.
	NoReadOnly bool

	// Keep leaves the container in place after the tool exits, which is cwltool's
	// --leave-container, the opt-out from its --rm-container default. It is a debugging aid: the
	// stopped container can be inspected, and nothing collects it afterwards.
	Keep bool
}

// container is the resolved container configuration of one invocation: which image, and which host
// directory is mounted at which path the tool sees.
type container struct {
	// image is the reference the tool is run from; see [imageReference].
	image string

	// hostOutdir, hostStage and hostTmpdir are the three directories on this host that are
	// mounted whole. They are the invocation's own output directory, the path map's staging
	// directory, and its scratch directory.
	hostOutdir string
	hostStage  string
	hostTmpdir string

	// toolOutdir is where hostOutdir appears inside the container: the tool's designated output
	// directory, and what runtime.outdir evaluates to.
	toolOutdir string

	// policy is what the caller asked this engine to do differently; see [ContainerPolicy]. It
	// is carried whole rather than unpacked into fields so that there is one answer to "what did
	// the caller ask for", and it is the same answer at every level of a run.
	policy ContainerPolicy
}

// newContainer resolves a DockerRequirement into the configuration one invocation runs under.
//
// It performs no I/O: the image is named, not fetched, so that an argv can be assembled and
// asserted without a container engine anywhere near. [ContainerExecutor.EnsureImage] is the half
// that needs one.
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

// containerStageName is the subdirectory of an invocation's scratch directory that holds what is
// staged for the container, kept apart from the scratch directory itself so that the two can be
// mounted at the two paths cwltool gives them.
const containerStageName = "stg"

// outdirFor resolves where the tool's designated output directory appears inside the container.
//
// DockerRequirement.dockerOutputDirectory names it, and the specification describes it as "a
// specific location inside the Docker container", which only an absolute path is. cwltool takes the
// field when it startswith("/") (process.py:900) and this follows that test rather than its `or`
// fallback, which would accept a relative path no mount can be built from.
func outdirFor(declared *cwlcore.DockerRequirement) string {
	if filepath.IsAbs(declared.DockerOutputDirectory) {
		return filepath.Clean(declared.DockerOutputDirectory)
	}

	return containerOutdir()
}

// mapper returns the path map an invocation running in this container plans with: targets under the
// directories the tool sees, hosts under the directories mounted at them.
func (c *container) mapper() *PathMap {
	return NewContainerPathMap(c.hostOutdir, c.hostStage, c.toolOutdir, containerStagedir)
}

// dirs creates the host directories that are mounted whole.
//
// They have to exist before the engine is asked to mount them, which is the whole reason cwltool's
// append_volume uses `--mount` rather than `--volume` and then makedirs the source itself: "Unlike
// `--volume`, `--mount` will fail if the volume doesn't already exist" (docker.py:236). Failing is
// the better half of that trade — `--volume` would silently create an empty directory owned by root
// and hand the tool that instead of the staged one — and it is what makes creating them here a
// requirement rather than a tidiness.
func (c *container) dirs() error {
	return os.MkdirAll(c.hostStage, stageDirPerm)
}

// containerSpec builds the structured container description from the resolved configuration and the
// path map's planned placements.
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

// containerMounts derives the bind mounts one invocation needs: the three directories that are
// mounted whole, and then whatever a planned placement could not be satisfied by.
//
// The three come first, as they do in cwltool's create_runtime, so that a placement's own mount is
// applied over the directory it falls inside rather than under it.
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

// containerWholeMounts is how many directories are mounted whole; see [container.containerMounts].
const containerWholeMounts = 3

// mountSource returns the host path one planned placement must be bind-mounted from, and whether it
// needs a mount at all.
//
// Two of the three placement kinds need one. A link is the case the mount exists for: [PathMap.Apply]
// leaves the bytes where they were and writes a symbolic link to them, which resolves on this host
// and would dangle inside the container, so the mount is what puts the real bytes at the path the
// tool was told to look at. This is cwltool's add_file_or_directory_volume.
//
// A placement that materializes real bytes — a copy, a written literal, a created directory —
// already put them inside one of the three directories mounted whole, and the tool reaches them
// through that mount. cwltool takes the same shortcut in add_writable_file_volume, copying into the
// output directory "which is already going to be mounted" instead of adding a second mount. Unless,
// that is, the placement's target is outside all three, which only an absolute `entryname` can be:
// then it is mounted from the host path the bytes were materialized at, read-write if the document
// asked for a writable entry, because that mount is the only route to them.
func (c *container) mountSource(mapping *PathMapping) (string, bool) {
	if mapping.Action == StageLink {
		return mapping.Resolved, true
	}

	if c.encloses(mapping.Target) {
		return "", false
	}

	return mapping.Host, true
}

// encloses reports whether a path the tool sees falls inside one of the directories mounted whole.
func (c *container) encloses(target string) bool {
	for _, dir := range []string{c.toolOutdir, containerStagedir, containerTmpdir} {
		if _, ok := relativeTo(dir, target); ok {
			return true
		}
	}

	return false
}
