package cwlexec

import (
	"archive/tar"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The image names the acquisition tests build and then remove. Each is distinct because the
// acquisition cache is process-wide and one test's success would otherwise answer another's
// question.
const (
	imgBuilt    = "cwl-go-test-built:latest"
	imgImported = "cwl-go-test-imported:latest"
	imgAbsent   = "cwl-go-test-absent:latest"
)

// imgBox returns a container that would run the given requirement's image.
func imgBox(declared *cwlcore.DockerRequirement) *container {
	return newContainer(declared, ctrHostOut, ctrHostTmp, ContainerPolicy{})
}

// imgRemove schedules an image for removal when the test ends, so that a rerun asks the engine the
// same question this run did.
//
// A removal that fails is logged rather than failed on: it is cleanup, the engine may already have
// collected the tag, and not tidying up is not a failure of what was being tested.
func imgRemove(t *testing.T, image string) {
	t.Helper()

	// The test's own context is cancelled before its cleanups run, and this one has work to do
	// after that: it is the removal that makes a rerun ask the engine what this run asked it.
	ctx := context.WithoutCancel(t.Context())

	t.Cleanup(func() {
		containerImages.Delete(image)

		err := exec.CommandContext(ctx, containerEngine, "rmi", "--force", image).Run()
		if err != nil {
			t.Logf("removing %s: %v", image, err)
		}
	})
}

func TestImageReferenceNamesWhatIsRun(t *testing.T) {
	t.Parallel()

	// cwltool's get_image opens by filling a missing dockerImageId in from dockerPull, so one
	// name is both the thing to run and the tag anything fetched is given.
	cases := []struct {
		declared *cwlcore.DockerRequirement
		name     string
		want     string
	}{{
		name:     "dockerImageId names it outright",
		declared: &cwlcore.DockerRequirement{DockerImageID: imgAbsent},
		want:     imgAbsent,
	}, {
		name:     "dockerPull names it when nothing else does",
		declared: &cwlcore.DockerRequirement{DockerPull: ctrImage},
		want:     ctrImage,
	}, {
		name: "dockerImageId wins, and is the tag a pull is given",
		declared: &cwlcore.DockerRequirement{
			DockerPull: ctrImage, DockerImageID: imgAbsent,
		},
		want: imgAbsent,
	}, {
		name:     "a Dockerfile alone names nothing to run",
		declared: &cwlcore.DockerRequirement{DockerFile: "FROM " + ctrImage},
		want:     "",
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := imageReference(testCase.declared); got != testCase.want {
				t.Errorf("imageReference = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestDockerImagePullRoundTrips(t *testing.T) {
	t.Parallel()

	// Daemon-required by design. A container test that skips itself when no engine is reachable
	// reports the same thing whether the feature works or was never exercised.
	declared := &cwlcore.DockerRequirement{DockerPull: ctrImage}
	box := imgBox(declared)

	err := box.acquire(t.Context(), declared)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if _, cached := containerImages.Load(ctrImage); !cached {
		t.Error("the image was acquired but not remembered, so a rerun would ask again")
	}

	// The second call is the cache, and must not reach the engine at all — which is what makes a
	// hundred scattered sub-jobs one question rather than a hundred.
	err = box.acquire(t.Context(), declared)
	if err != nil {
		t.Fatalf("acquire again: %v", err)
	}
}

func TestDockerImageBuildsFromADockerfile(t *testing.T) {
	t.Parallel()

	imgRemove(t, imgBuilt)

	// dockerFile: "Supply the contents of a Dockerfile which will be built using `docker build`."
	// It is built unconditionally and before anything else is consulted, because the Dockerfile
	// is the definition of the image rather than a way of obtaining one that already exists.
	declared := &cwlcore.DockerRequirement{
		DockerFile:    "FROM " + ctrImage + "\nRUN touch /built\n",
		DockerImageID: imgBuilt,
	}

	box := imgBox(declared)

	err := box.acquire(t.Context(), declared)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if !box.present(t.Context()) {
		t.Errorf("%s was built but the engine does not have it", imgBuilt)
	}
}

func TestDockerImageImportsATarball(t *testing.T) {
	t.Parallel()

	imgRemove(t, imgImported)

	declared := &cwlcore.DockerRequirement{
		DockerImport:  imgTarball(t),
		DockerImageID: imgImported,
	}

	box := imgBox(declared)

	err := box.acquire(t.Context(), declared)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if !box.present(t.Context()) {
		t.Errorf("%s was imported but the engine does not have it", imgImported)
	}
}

// imgTarball writes a one-file filesystem tarball, which is what dockerImport takes: "a tarball
// which will be loaded using `docker import`".
func imgTarball(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rootfs.tar")

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the tarball: %v", err)
	}

	archive := tar.NewWriter(file)
	body := []byte(execGreeting)

	err = archive.WriteHeader(&tar.Header{
		Name: execOutName, Mode: int64(stageFilePerm), Size: int64(len(body)),
	})
	if err == nil {
		_, err = archive.Write(body)
	}

	err = errors.Join(err, archive.Close(), file.Close())
	if err != nil {
		t.Fatalf("writing the tarball: %v", err)
	}

	return path
}

func TestDockerImageAcquisitionFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		declared *cwlcore.DockerRequirement
		want     error
		name     string
	}{{
		name:     "a requirement naming no image at all",
		declared: &cwlcore.DockerRequirement{},
		want:     ErrContainerImage,
	}, {
		name:     "an image that is not here and nothing that says where to get it",
		declared: &cwlcore.DockerRequirement{DockerImageID: imgAbsent},
		want:     ErrContainerImage,
	}, {
		name: "an archive this engine cannot read",
		declared: &cwlcore.DockerRequirement{
			DockerImageID: imgAbsent, DockerLoad: "/nonexistent/image.tar",
		},
		want: ErrContainerImage,
	}, {
		name: "an archive named by a file URL, which is a path after all",
		declared: &cwlcore.DockerRequirement{
			DockerImageID: imgAbsent, DockerLoad: "file:///nonexistent/image.tar",
		},
		want: ErrContainerImage,
	}, {
		name: "an archive that would have to be downloaded",
		declared: &cwlcore.DockerRequirement{
			DockerImageID: imgAbsent, DockerLoad: "https://example.invalid/image.tar",
		},
		want: ErrUnsupportedFeature,
	}, {
		name: "an archive whose name is not a reference at all",
		declared: &cwlcore.DockerRequirement{
			DockerImageID: imgAbsent, DockerLoad: "/nonexistent/\x7f.tar",
		},
		want: ErrContainerImage,
	}, {
		name: "a Dockerfile that does not build",
		declared: &cwlcore.DockerRequirement{
			DockerImageID: imgAbsent, DockerFile: "NOT-AN-INSTRUCTION\n",
		},
		want: ErrContainerImage,
	}, {
		name:     "a registry that does not have it",
		declared: &cwlcore.DockerRequirement{DockerPull: "cwl-go-test/nothing:here"},
		want:     ErrContainerImage,
	}, {
		name: "a tarball that is not one",
		declared: &cwlcore.DockerRequirement{
			DockerImageID: imgAbsent, DockerImport: "/nonexistent/rootfs.tar",
		},
		want: ErrContainerImage,
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := imgBox(testCase.declared).acquire(t.Context(), testCase.declared)
			if !errors.Is(err, testCase.want) {
				t.Errorf("acquire: error %v does not wrap %v", err, testCase.want)
			}
		})
	}
}

func TestDockerImageNeedsAnEngineOnPath(t *testing.T) {
	// Not parallel: it edits this process's own environment, and Go runs the sequential tests
	// alone. An engine that is not installed is the one condition in image acquisition that is
	// genuinely a feature this machine does not have, so it is ErrUnsupportedFeature — which
	// cmd/cwl-run turns into the cwl-runner contract's exit 33 and the harness reads as a skip.
	t.Setenv(envPath, "")

	declared := &cwlcore.DockerRequirement{DockerPull: "cwl-go-test/no-engine:here"}

	err := imgBox(declared).acquire(t.Context(), declared)
	if !errors.Is(err, ErrUnsupportedFeature) {
		t.Errorf("acquire: error %v does not wrap %v", err, ErrUnsupportedFeature)
	}
}

func TestDockerImageBuildNeedsAContextDirectory(t *testing.T) {
	// Also not parallel, for the same reason. A Dockerfile is built from a directory of its own
	// because that directory is the build context, and there is nowhere to put one here.
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "nonexistent"))

	declared := &cwlcore.DockerRequirement{
		DockerImageID: "cwl-go-test/no-context:here",
		DockerFile:    "FROM " + ctrImage + "\n",
	}

	err := imgBox(declared).acquire(t.Context(), declared)
	if err == nil {
		t.Error("acquire succeeded with nowhere to write the Dockerfile")
	}
}
