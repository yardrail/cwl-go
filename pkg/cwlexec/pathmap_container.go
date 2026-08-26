package cwlexec

import (
	"fmt"
	"path/filepath"
	"strings"
)

// The container half of [PathMap].
//
// Everything here exists because a tool inside a container sees a filesystem this process does not
// have. The plan is made in the tool's terms — its targets are what goes into the argv, the
// expressions and the rewritten input object — and each placement carries alongside it the host
// path [PathMap.Apply] writes to; deriving the second from the first is most of what follows.
//
// [PathMap.targetIn] is here for the same reason, being the one place a document can name a target
// the mapper's own directories do not contain.

// outsideName is the subdirectory of a container mapper's host staging directory that holds
// anything planned at a target outside both of the directories mounted whole — a literal written
// under an absolute `entryname`, say. The bytes have to exist somewhere on this host for a bind
// mount to have a source, and this is where.
const outsideName = ".outside"

// NewContainerPathMap returns an empty path map for an invocation whose tool runs in its own
// filesystem namespace.
//
// toolWorkdir and toolStaging are the two directories the *tool* sees; hostWorkdir and hostStaging
// are the directories on this host that will be mounted there. Everything the map plans is planned
// at a path the tool will see, because that is what goes into the argv, the expressions and the
// rewritten input object — and each placement carries alongside it the host path [PathMap.Apply]
// must write to, derived from whichever of the two mounted directories its target falls in.
//
// A target in neither is a placement the executor must bind-mount for itself: an absolute
// `entryname`, which is legal only here. When its bytes already exist — a staged input file — there
// is nothing for this host to do and its Host is empty. When they do not, they are materialized
// under hostStaging so that the mount has a source.
func NewContainerPathMap(hostWorkdir, hostStaging, toolWorkdir, toolStaging string) *PathMap {
	mapper := NewPathMap(toolWorkdir, toolStaging)
	mapper.hostWorkdir, mapper.hostStaging = hostWorkdir, hostStaging
	mapper.contained = true

	return mapper
}

// AllowAbsoluteTargets records that a listing entry may name a target outside the working directory
// altogether, which is what an absolute `entryname` does.
//
// CommandLineTool.yml, Dirent.entryname: "If the value is an absolute path starting with `/` it
// will be assumed to be an absolute path... this is only valid if the program is will run inside a
// software container where, from the perspective of the program, the root filesystem is not shared
// with any other user or running program."
//
// The condition is narrower than "a container is in use", and cwltool draws the line where the
// sentence does. `add_volumes` refuses a target outside the container's output directory unless
// `any_path_okay`, and `DockerCommandLineJob.create_runtime` sets that from the *second* element of
// `get_requirement("DockerRequirement")` — which is whether the declaration was a requirement
// rather than a hint. A hint is a declaration the engine is free to ignore, so a document may not
// rely on it having been honoured; conformance tests iwd-container-entryname1 (a requirement,
// passes), 2 (no declaration) and 3 (a hint) are the three cases written out.
//
// It is a method rather than a [NewContainerPathMap] parameter for the same reason
// [PathMap.AllowInplaceUpdate] is: the caller resolves the declaration from a scope the map does
// not have.
func (m *PathMap) AllowAbsoluteTargets() {
	m.absolute = true
}

// hostFor derives where [PathMap.Apply] must write for the tool to find a value at target, or ""
// when there is nothing for this host to do.
//
// Three cases, and only the first exists without a container. A target under one of the two
// directories the executor mounts whole keeps its position inside the host directory mounted there,
// which is what makes one Apply serve both kinds of invocation. A target outside both is an
// absolute `entryname`: bytes that have to be created are created under the staging directory so
// that the mount the executor adds has a source, and bytes that already exist need no host path at
// all, because that mount is the whole of what places them.
//
// The last case is where cwltool's relink_initialworkdir comes in. A container engine creates a
// missing mount point, so a value bind-mounted into the output directory leaves an empty file
// behind on this host when the container exits — which output collection would then glob. cwltool
// deletes it afterwards and symlinks the real source in its place; planning the same symlink here
// up front reaches the same state, and the engine mounts over it rather than creating anything.
func (m *PathMap) hostFor(target string, action StageAction) string {
	if action == StageLink && m.outside(target) {
		return ""
	}

	return m.hostPath(target)
}

// hostPath returns where a path the tool sees is reached on this host. It is the identity without a
// container, where the tool sees this host's own filesystem and there is nothing to translate.
func (m *PathMap) hostPath(target string) string {
	if !m.contained {
		return target
	}

	if rest, ok := relativeTo(m.workdir, target); ok {
		return filepath.Join(m.hostWorkdir, rest)
	}

	if rest, ok := relativeTo(m.staging, target); ok {
		return filepath.Join(m.hostStaging, rest)
	}

	return filepath.Join(m.hostStaging, outsideName, target)
}

// hostSource returns where the bytes a tool sees at target are on this host *while the tool runs*,
// which is not always where [PathMap.hostPath] says the placement for them lives.
//
// The two differ for exactly one placement: a [StageLink] under a container, whose host path is
// staged as an empty mount point. Its bytes arrive over that point as a bind mount and are restored
// to it as a symbolic link by [PathMap.Relink], but neither has happened yet at the moment a
// redirection is opened — RunProcess opens it on this host and the engine inherits the descriptor,
// both before the container starts. Opening the placement would connect the tool's standard input
// to an empty file. Resolved is where the bytes are the whole time, so that is what is opened.
//
// Without a container the answer is the same one by a different route, which is why there is no
// branch on it here: Apply placed a symbolic link to Resolved, so reading either reads the same
// bytes.
func (m *PathMap) hostSource(target string) string {
	for index := range m.plan {
		mapping := &m.plan[index]

		if mapping.Target == target && mapping.Action == StageLink && mapping.Resolved != "" {
			return mapping.Resolved
		}
	}

	return m.hostPath(target)
}

// hostOutputPath maps a path a tool named in its own output object back to this host's filesystem.
//
// It differs from [PathMap.hostPath] in what it does with a path the map does not span, and the
// difference matters because the two are asked different questions. hostPath is asked where a
// placement this map *planned* must be written, so a target outside both directories is an absolute
// `entryname` and belongs under the staging directory. Here the path was chosen by the tool rather
// than planned, and one outside both directories is simply a path this map has nothing to say
// about — relocating it under .outside would invent a host path for it. It is returned unchanged
// and left to [outputCollector.checkPublishable], which is what decides whether a tool may name it.
func (m *PathMap) hostOutputPath(target string) string {
	if !m.contained || m.outside(target) {
		return target
	}

	return m.hostPath(target)
}

// outside reports whether a path the tool sees falls in neither of the two directories this map
// plans into, which without a container nothing can and with one only an absolute `entryname` can.
func (m *PathMap) outside(target string) bool {
	if !m.contained {
		return false
	}

	_, inWorkdir := relativeTo(m.workdir, target)
	_, inStaging := relativeTo(m.staging, target)

	return !inWorkdir && !inStaging
}

// relativeTo returns local's position under dir, and whether it is under it at all. Both are
// compared as the cleaned absolute paths this package deals in.
func relativeTo(dir, local string) (string, bool) {
	if local == dir {
		return "", true
	}

	prefix := dir + string(filepath.Separator)
	if !strings.HasPrefix(local, prefix) {
		return "", false
	}

	return local[len(prefix):], true
}

// targetIn resolves a document-supplied entry name against a base directory, rejecting anything that
// would put the entry somewhere else.
//
// An absolute name is the one way out of the working directory the specification allows, and only
// under the condition [PathMap.AllowAbsoluteTargets] records. Without it the name is refused as the
// document error it is: it names a path on a filesystem the tool shares with everything else on this
// machine, and writing there is exactly what the sentence quoted there forbids.
func (m *PathMap) targetIn(base, name string) (string, error) {
	if filepath.IsAbs(name) {
		if !m.absolute {
			return "", fmt.Errorf(
				"%w: an absolute entryname (%q) is valid only with DockerRequirement in requirements",
				ErrStagePath, name)
		}

		return filepath.Clean(name), nil
	}

	if name == "" || !filepath.IsLocal(name) {
		return "", fmt.Errorf("%w: %q", ErrStagePath, name)
	}

	return filepath.Join(base, name), nil
}

// hostView returns a map that relocates an input object the other way: from the paths the tool sees
// back to the paths this host has.
//
// Output collection needs it, and for a reason that is easy to miss. [CollectOutputs] reads the
// input object for two things — the expressions a binding carries, and the set of paths the
// invocation brought into its working directory. The second is a containment test against real
// files: a staged input is placed as a symbolic link, so a tool naming one as its own output
// produces a link leading out of the output directory, and the test that admits it compares the
// link's target against those paths *with their own symlinks resolved*. A container path resolves
// to nothing here, so the test would reject a value the specification requires to be published.
//
// It is built as a path map rather than as a second walk because relocating an input object is
// already [PathMap.RewriteInputs]'s whole job; only the lookup it consults differs.
func (m *PathMap) hostView() *PathMap {
	back := NewPathMap(m.hostWorkdir, m.hostStaging)

	for index := range m.plan {
		mapping := &m.plan[index]

		// A placement with no host path of its own is one the executor bind-mounts from
		// Resolved, which is where its bytes are on this host.
		host := mapping.Host
		if host == "" {
			host = mapping.Resolved
		}

		if _, claimed := back.byPath[mapping.Target]; !claimed {
			back.byPath[mapping.Target] = host
		}
	}

	return back
}
