package cwlexec

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Container-aware path mapping: translates between tool-side and host-side paths.

// outsideName holds materialized bytes for targets outside mounted directories.
const outsideName = ".outside"

// NewContainerPathMap returns a path map for a containerized invocation.
// Targets are in the tool's namespace; hosts are derived from the mounted directories.
func NewContainerPathMap(hostWorkdir, hostStaging, toolWorkdir, toolStaging string) *PathMap {
	mapper := NewPathMap(toolWorkdir, toolStaging)
	mapper.hostWorkdir, mapper.hostStaging = hostWorkdir, hostStaging
	mapper.contained = true

	return mapper
}

// AllowAbsoluteTargets permits absolute entryname targets (requires DockerRequirement).
func (m *PathMap) AllowAbsoluteTargets() {
	m.absolute = true
}

// hostFor returns the host path Apply must write to, or "" if bind-mounted directly.
func (m *PathMap) hostFor(target string, action StageAction) string {
	if action == StageLink && m.outside(target) {
		return ""
	}

	return m.hostPath(target)
}

// hostPath translates a tool-side path to a host path. Identity without a container.
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

// hostBytesOf returns the durable host path: Resolved for links, Host for materialized entries.
func hostBytesOf(mapping *PathMapping) string {
	if mapping.Action == StageLink && mapping.Resolved != "" {
		return mapping.Resolved
	}

	return mapping.Host
}

// hostSource returns the host path of the actual bytes for target (before mounts are applied).
func (m *PathMap) hostSource(target string) string {
	for index := range m.plan {
		mapping := &m.plan[index]

		if mapping.Target == target && mapping.Action == StageLink && mapping.Resolved != "" {
			return hostBytesOf(mapping)
		}
	}

	return m.hostPath(target)
}

// hostOutputPath maps a tool-produced output path to the host. Paths outside known mounts are unchanged.
func (m *PathMap) hostOutputPath(target string) string {
	if !m.contained || m.outside(target) {
		return target
	}

	return m.hostPath(target)
}

// outside reports whether target falls outside both mapped directories.
func (m *PathMap) outside(target string) bool {
	if !m.contained {
		return false
	}

	_, inWorkdir := relativeTo(m.workdir, target)
	_, inStaging := relativeTo(m.staging, target)

	return !inWorkdir && !inStaging
}

// relativeTo returns local's position under dir, and whether it is under dir.
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

// targetIn resolves an entry name against base, rejecting escapes unless absolute targets are allowed.
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

// hostView returns a reverse map: tool-side paths → host-side durable byte paths.
func (m *PathMap) hostView() *PathMap {
	back := NewPathMap(m.hostWorkdir, m.hostStaging)

	for index := range m.plan {
		mapping := &m.plan[index]

		host := hostBytesOf(mapping)

		if _, claimed := back.byPath[mapping.Target]; !claimed {
			back.byPath[mapping.Target] = host
		}
	}

	return back
}
