package cwlexec

import "testing"

// TestContainerSpecDirAccessors pins the role-based contract: the runtime
// directories are found by Mount.Role regardless of position.
func TestContainerSpecDirAccessors(t *testing.T) {
	t.Parallel()

	spec := &ContainerSpec{
		Mounts: []Mount{
			{Source: "/data/input.bam", Target: "/work/input.bam", ReadOnly: true, Role: MountRoleData},
			{Source: "/host/stage", Target: "/var/lib/cwl", Role: MountRoleStage},
			{Source: "/host/tmp", Target: "/tmp", Role: MountRoleTmp},
			{Source: "/host/out", Target: "/work", Role: MountRoleOut},
		},
	}

	if got := spec.OutDir(); got != "/host/out" {
		t.Errorf("OutDir() = %q, want %q", got, "/host/out")
	}

	if got := spec.TmpDir(); got != "/host/tmp" {
		t.Errorf("TmpDir() = %q, want %q", got, "/host/tmp")
	}

	if got := spec.StagingDir(); got != "/host/stage" {
		t.Errorf("StagingDir() = %q, want %q", got, "/host/stage")
	}

	outMount, ok := spec.MountByRole(MountRoleOut)
	if !ok || outMount.Target != "/work" {
		t.Errorf("MountByRole(out) = %+v, %v; want Target /work, true", outMount, ok)
	}

	if _, ok := (&ContainerSpec{}).MountByRole(MountRoleOut); ok {
		t.Error("MountByRole on empty spec reported ok")
	}
}

func TestContainerSpecDirAccessorsAbsent(t *testing.T) {
	t.Parallel()

	for _, spec := range []*ContainerSpec{
		{},
		{Mounts: []Mount{{Source: "/data", Target: "/work/data", Role: MountRoleData}}},
	} {
		if got := spec.OutDir(); got != "" {
			t.Errorf("OutDir() = %q, want empty", got)
		}

		if got := spec.TmpDir(); got != "" {
			t.Errorf("TmpDir() = %q, want empty", got)
		}

		if got := spec.StagingDir(); got != "" {
			t.Errorf("StagingDir() = %q, want empty", got)
		}
	}
}

// TestContainerSpecAccessorsFromConstruction ties the accessors to the real
// construction path in containerMounts.
func TestContainerSpecAccessorsFromConstruction(t *testing.T) {
	t.Parallel()

	box := ctrBox()
	spec := box.containerSpec(nil, false)

	if got := spec.OutDir(); got != ctrHostOut {
		t.Errorf("OutDir() = %q, want %q", got, ctrHostOut)
	}

	if got := spec.TmpDir(); got != ctrHostTmp {
		t.Errorf("TmpDir() = %q, want %q", got, ctrHostTmp)
	}

	if got := spec.StagingDir(); got != ctrHostStage {
		t.Errorf("StagingDir() = %q, want %q", got, ctrHostStage)
	}
}
