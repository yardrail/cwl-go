package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Getting the image a DockerRequirement names.
//
// The requirement has five fields naming an image and they are alternatives, not a sequence: one
// says what to run, three say where to get it from, and the fifth is both. cwltool's get_image is
// what settles the order they are consulted in, and this file follows it.

// ErrContainerImage reports an image a DockerRequirement names that could not be made available.
//
// It is a failure rather than a missing feature: the engine is here, the requirement is understood,
// and the image is what is wrong — a registry that does not have it, a Dockerfile that does not
// build, a name that matches nothing local. A container engine that is not installed at all is
// reported as [ErrUnsupportedFeature] instead, which is what makes a document declaring a container
// skip on a machine without one rather than fail on it.
var ErrContainerImage = errors.New("container image is not available")

// containerImages remembers the references this process has already made available, so that a
// scattered step's hundred sub-jobs do not each ask a registry the same question.
//
// cwltool keeps the same set behind the same reasoning. It is only ever added to: an image that was
// present a moment ago is not going to stop being present during one run, and re-checking would put
// a subprocess in front of every invocation to learn nothing.
var containerImages sync.Map

// dockerBuildPrefix names the temporary directory a `dockerFile` is built from.
const dockerBuildPrefix = "cwl-docker-build-"

// imageReference is the name the tool is run from, and the tag anything built, loaded or imported
// is given.
//
// cwltool's get_image opens by filling a missing dockerImageId in from dockerPull (docker.py:113),
// which is what makes one name serve both purposes: `dockerPull: alpine` runs `alpine`, and
// `dockerFile` with `dockerImageId: mine` builds and then runs `mine`. dockerImageId on its own
// means "run what is already here", and is the only field that names nothing to fetch.
func imageReference(declared *cwlcore.DockerRequirement) string {
	if declared.DockerImageID != "" {
		return declared.DockerImageID
	}

	return declared.DockerPull
}

// acquire makes the image available to the engine, doing nothing when it already is.
func (c *container) acquire(ctx context.Context, declared *cwlcore.DockerRequirement) error {
	if c.image == "" {
		return fmt.Errorf("%w: DockerRequirement names no image", ErrContainerImage)
	}

	if _, cached := containerImages.Load(c.image); cached {
		return nil
	}

	err := c.fetch(ctx, declared)
	if err != nil {
		return err
	}

	containerImages.Store(c.image, true)

	return nil
}

// fetch runs whichever of the image-source fields applies, in cwltool's precedence.
//
// A dockerFile is built unconditionally and first, which is cwltool's order and not an oversight:
// the Dockerfile is the definition of the image, so an older build of the same tag is not the thing
// the document asked for. The four that follow are each a way of *obtaining* an image that already
// exists somewhere, so they run only when this engine does not already have it — which is where
// dockerImageId alone lands, having nothing to obtain it with.
func (c *container) fetch(ctx context.Context, declared *cwlcore.DockerRequirement) error {
	if declared.DockerFile != "" {
		return c.build(ctx, declared.DockerFile)
	}

	if c.present(ctx) {
		return nil
	}

	switch {
	case declared.DockerPull != "":
		return c.engine(ctx, "pull", declared.DockerPull)
	case declared.DockerLoad != "":
		return c.load(ctx, declared.DockerLoad)
	case declared.DockerImport != "":
		return c.engine(ctx, "import", declared.DockerImport, c.image)
	default:
		return fmt.Errorf("%w: %s is not present and nothing says where to get it",
			ErrContainerImage, c.image)
	}
}

// present reports whether the engine already holds the image, which is cwltool's `docker inspect`
// on the dockerImageId. A failure means "not here" rather than "broken": inspect is the question,
// and a non-zero answer is one of the two answers it has.
func (c *container) present(ctx context.Context) bool {
	return c.engine(ctx, "inspect", c.image) == nil
}

// build builds the image from a literal Dockerfile, which is what DockerRequirement.dockerFile
// holds: "Supply the contents of a Dockerfile which will be built using `docker build`."
//
// The contents are written into a directory of their own because that directory is the build
// context, and a Dockerfile's COPY reads from it. Nothing is put in it, so a document whose
// Dockerfile copies anything at all fails — as it must, there being nowhere for a relative path in
// it to be relative *to*. cwltool builds from an equally empty temporary directory.
func (c *container) build(ctx context.Context, dockerfile string) error {
	dir, err := os.MkdirTemp("", dockerBuildPrefix)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), stageFilePerm)
	if err == nil {
		err = c.engine(ctx, "build", "--tag="+c.image, dir)
	}

	return errors.Join(err, os.RemoveAll(dir))
}

// load loads a saved image archive, which is what DockerRequirement.dockerLoad names.
//
// The schema calls the field an "HTTP URL from which to download Docker image", and cwltool accepts
// a local path there as well, streaming either into `docker load`. Only the local forms are
// supported here — a plain path, or a `file:` URL — and an http URL is reported as the missing
// feature it is.
//
// The line is drawn where the rest of this engine draws it. Nothing in cwlexec fetches over the
// network: a File whose `location` names a scheme it cannot read from is [ErrUnsupportedFeature]
// too (see PathMap.stageFile), and an image archive is not a better reason to open a socket than a
// tool's input file is. dockerImport, whose engine subcommand takes a URL itself, is unaffected.
func (c *container) load(ctx context.Context, source string) error {
	local, err := localArchive(source)
	if err != nil {
		return err
	}

	return c.engine(ctx, "load", "--input", local)
}

// localArchive resolves what dockerLoad names to a path on this filesystem.
//
// A value with no scheme is a path, which is the form cwltool's os.path.exists test accepts; a
// `file:` URL is the same path written as the schema's IRI; anything else names a resource
// somewhere this engine does not fetch from.
func localArchive(source string) (string, error) {
	scheme, rest, written := strings.Cut(source, schemeSeparator)
	if !written {
		return filepath.Clean(source), nil
	}

	if scheme != joSchemeFile {
		return "", fmt.Errorf("%w: dockerLoad from %s (downloading an image archive is not implemented)",
			ErrUnsupportedFeature, source)
	}

	return filepath.Clean("/" + strings.TrimLeft(rest, "/")), nil
}

// schemeSeparator is what divides a URL's scheme from the rest of it.
const schemeSeparator = "://"

// engine runs one container-engine subcommand and reports what it said if it failed.
//
// The subcommand is a parameter of its own rather than the first of the variadic arguments so that
// naming it in the error message needs no index into a slice a caller could have left empty.
//
// The program is looked up here, from a constant name, rather than carried on the container: a
// program path that has travelled through a struct field is one gosec cannot prove safe to execute,
// and there is no need for it to travel. An engine that is not installed is [ErrUnsupportedFeature]
// — the one condition in this file that is genuinely a feature this machine does not have.
func (c *container) engine(ctx context.Context, command string, args ...string) error {
	program, err := exec.LookPath(containerEngine)
	if err != nil {
		return fmt.Errorf("%w: %s is not on PATH: %w", ErrUnsupportedFeature, containerEngine, err)
	}

	output, err := exec.CommandContext(ctx, program, append([]string{command}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w: %s", ErrContainerImage,
			containerEngine, command, err, strings.TrimSpace(string(output)))
	}

	return nil
}
