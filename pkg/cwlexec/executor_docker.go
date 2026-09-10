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

// The Docker CLI container executor.
//
// This is the default [ContainerExecutor]: it shells out to the `docker` binary, building the same
// `docker run` argv the engine has always built, and acquiring images through `docker pull`,
// `docker build`, `docker load` and `docker import`. Its behaviour is byte-for-byte identical to
// what the engine did before the ContainerExecutor interface existed, so existing callers and the
// conformance suite see no change.

// dockerEngine is the program a DockerRequirement is carried out with.
//
// It is a constant, and looked up from one, because a program name that has travelled through a
// struct field is one gosec cannot prove safe to execute. Podman is deliberately out of scope: it
// is a second runtime for the same argv rather than a second thing to model, and adding it means
// choosing between them, not teaching this file anything new.
const dockerEngine = "docker"

// dockerRun is the engine subcommand that starts a container.
const dockerRun = "run"

// ErrContainerImage reports an image a DockerRequirement names that could not be made available.
//
// It is a failure rather than a missing feature: the engine is here, the requirement is understood,
// and the image is what is wrong — a registry that does not have it, a Dockerfile that does not
// build, a name that matches nothing local. A container engine that is not installed at all is
// reported as [ErrUnsupportedFeature] instead, which is what makes a document declaring a container
// skip on a machine without one rather than fail on it.
var ErrContainerImage = errors.New("container image is not available")

// dockerImages remembers the references this process has already made available, so that a
// scattered step's hundred sub-jobs do not each ask a registry the same question.
//
// cwltool keeps the same set behind the same reasoning. It is only ever added to: an image that was
// present a moment ago is not going to stop being present during one run, and re-checking would put
// a subprocess in front of every invocation to learn nothing.
var dockerImages sync.Map

// dockerBuildPrefix names the temporary directory a `dockerFile` is built from.
const dockerBuildPrefix = "cwl-docker-build-"

// Compile-time proof that the executor satisfies the contract.
var _ ContainerExecutor = (*DockerCLIExecutor)(nil)

// DockerCLIExecutor is the default [ContainerExecutor]: it shells out to the `docker` CLI binary,
// preserving the subprocess behaviour the engine has always had. The zero value is usable.
type DockerCLIExecutor struct{}

// NewDockerCLIExecutor returns a new [DockerCLIExecutor].
func NewDockerCLIExecutor() *DockerCLIExecutor { return &DockerCLIExecutor{} }

// EnsureImage makes the image a DockerRequirement names available to the local Docker daemon,
// following cwltool's precedence: build unconditionally if dockerFile is set; otherwise check
// whether the image is already present, then pull / load / import.
func (d *DockerCLIExecutor) EnsureImage(ctx context.Context, req *cwlcore.DockerRequirement) error {
	image := imageReference(req)
	if image == "" {
		return fmt.Errorf("%w: DockerRequirement names no image", ErrContainerImage)
	}

	if _, cached := dockerImages.Load(image); cached {
		return nil
	}

	err := d.fetch(ctx, image, req)
	if err != nil {
		return err
	}

	dockerImages.Store(image, true)

	return nil
}

// Run executes a tool inside a container described by ctr, returning its exit code.
//
// It builds the same `docker run` argv the engine has always built, opens the process's standard
// streams on this host, and spawns the docker client as a child process. The redirections are
// deliberately handled on this host: RunProcess opens them here and the client inherits the file
// descriptors, passing the container's own standard streams straight through, so the tool's output
// reaches the same file whether or not a container is in the way.
func (d *DockerCLIExecutor) Run(
	ctx context.Context, ctr *ContainerSpec, spec *ProcessSpec,
) (int, error) {
	argv := d.dockerArgv(ctr, spec)

	wrapped := *spec
	wrapped.Command = &CommandLine{Args: dockerPlainArgs(argv), Shell: false}
	wrapped.Env = os.Environ()

	return RunProcess(ctx, &wrapped)
}

// fetch runs whichever of the image-source fields applies, in cwltool's precedence.
func (d *DockerCLIExecutor) fetch(
	ctx context.Context, image string, req *cwlcore.DockerRequirement,
) error {
	if req.DockerFile != "" {
		return d.build(ctx, image, req.DockerFile)
	}

	if d.present(ctx, image) {
		return nil
	}

	switch {
	case req.DockerPull != "":
		return d.engine(ctx, "pull", req.DockerPull)
	case req.DockerLoad != "":
		return d.load(ctx, req.DockerLoad)
	case req.DockerImport != "":
		return d.engine(ctx, "import", req.DockerImport, image)
	default:
		return fmt.Errorf("%w: %s is not present and nothing says where to get it",
			ErrContainerImage, image)
	}
}

// present reports whether the engine already holds the image.
func (d *DockerCLIExecutor) present(ctx context.Context, image string) bool {
	return d.engine(ctx, "inspect", image) == nil
}

// build builds the image from a literal Dockerfile.
func (d *DockerCLIExecutor) build(ctx context.Context, image, dockerfile string) error {
	dir, err := os.MkdirTemp("", dockerBuildPrefix)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), stageFilePerm)
	if err == nil {
		err = d.engine(ctx, "build", "--tag="+image, dir)
	}

	return errors.Join(err, os.RemoveAll(dir))
}

// load loads a saved image archive from a local path or file: URL.
func (d *DockerCLIExecutor) load(ctx context.Context, source string) error {
	local, err := dockerLocalArchive(source)
	if err != nil {
		return err
	}

	return d.engine(ctx, "load", "--input", local)
}

// engine runs one container-engine subcommand and reports what it said if it failed.
//
// The program is looked up here, from a constant name, rather than carried on a struct: a
// program path that has travelled through a struct field is one gosec cannot prove safe to execute,
// and there is no need for it to travel. An engine that is not installed is [ErrUnsupportedFeature]
// — the one condition that is genuinely a feature this machine does not have.
func (d *DockerCLIExecutor) engine(ctx context.Context, command string, args ...string) error {
	program, err := exec.LookPath(dockerEngine)
	if err != nil {
		return fmt.Errorf("%w: %s is not on PATH: %w", ErrUnsupportedFeature, dockerEngine, err)
	}

	output, err := exec.CommandContext(ctx, program, append([]string{command}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w: %s", ErrContainerImage,
			dockerEngine, command, err, strings.TrimSpace(string(output)))
	}

	return nil
}

// dockerArgv builds the `docker run` argument vector from a [ContainerSpec] and a [ProcessSpec].
func (d *DockerCLIExecutor) dockerArgv(ctr *ContainerSpec, spec *ProcessSpec) []string {
	argv := []string{dockerEngine, dockerRun, "-i"}
	argv = append(argv, dockerMountArgs(ctr.Mounts)...)
	argv = append(argv, "--workdir="+ctr.WorkDir)
	argv = append(argv, dockerReadOnlyModes[ctr.ReadOnlyRoot]...)
	argv = append(argv, dockerNetworkModes[ctr.NetworkAccess]...)
	argv = append(argv, dockerLogArgs(ctr.Stdout)...)
	argv = append(argv, dockerUserArgs(ctr)...)
	argv = append(argv, dockerRemoveModes[ctr.Remove]...)
	argv = append(argv, dockerEnvArgs(spec.Env)...)
	argv = append(argv, ctr.Image)
	argv = append(argv, spec.argv()...)

	return argv
}

// dockerLocalArchive resolves what dockerLoad names to a path on this filesystem.
func dockerLocalArchive(source string) (string, error) {
	scheme, rest, written := strings.Cut(source, dockerSchemeSeparator)
	if !written {
		return filepath.Clean(source), nil
	}

	if scheme != joSchemeFile {
		return "", fmt.Errorf("%w: dockerLoad from %s (downloading an image archive is not implemented)",
			ErrUnsupportedFeature, source)
	}

	return filepath.Clean("/" + strings.TrimLeft(rest, "/")), nil
}

// dockerSchemeSeparator is what divides a URL's scheme from the rest of it.
const dockerSchemeSeparator = "://"

// dockerMountArgs renders each mount as a --mount flag.
func dockerMountArgs(mounts []Mount) []string {
	args := make([]string, 0, len(mounts))

	for index := range mounts {
		mount := &mounts[index]

		var mode []string
		if mount.ReadOnly {
			mode = []string{"readonly"}
		}

		args = append(args, dockerMountArg(mount.Source, mount.Target, mode...))
	}

	return args
}

// dockerMountArg renders one bind mount.
//
// The `--mount` spelling is cwltool's append_volume, and its comment is the reason: "Unlike
// `--volume`, `--mount` will fail if the volume doesn't already exist". A mount that fails loudly
// beats one that quietly invents an empty directory where a staged input was supposed to be.
func dockerMountArg(source, target string, mode ...string) string {
	options := append([]string{"type=bind", "source=" + source, "target=" + target}, mode...)

	quoted := make([]string, 0, len(options))
	for _, option := range options {
		quoted = append(quoted, dockerCSVField(option))
	}

	return "--mount=" + strings.Join(quoted, ",")
}

// dockerCSVField quotes one field of a CSV record, as encoding/csv would: a field holding a
// separator or a quote is wrapped in quotes, and its own quotes are doubled.
func dockerCSVField(field string) string {
	if !strings.ContainsAny(field, `,"`) {
		return field
	}

	return `"` + strings.ReplaceAll(field, `"`, `""`) + `"`
}

// dockerNetworkModes maps NetworkAccess onto the Docker argument.
var dockerNetworkModes = map[bool][]string{true: nil, false: {"--net=none"}}

// dockerReadOnlyModes maps ReadOnlyRoot onto the Docker argument.
var dockerReadOnlyModes = map[bool][]string{true: {"--read-only=true"}, false: nil}

// dockerRemoveModes maps Remove onto the Docker argument.
var dockerRemoveModes = map[bool][]string{true: {"--rm"}, false: nil}

// dockerLogArgs turns off the engine's own log capture when the tool's standard output is already
// being written to a file.
func dockerLogArgs(stdout string) []string {
	if stdout == "" {
		return nil
	}

	return []string{"--log-driver=none"}
}

// dockerUserArgs runs the tool as the user that started this engine rather than as the image's own.
func dockerUserArgs(ctr *ContainerSpec) []string {
	if !ctr.MatchUser {
		return nil
	}

	return []string{fmt.Sprintf("--user=%d:%d", os.Geteuid(), os.Getgid())}
}

// dockerEnvArgs hands the tool's resolved environment to the container.
func dockerEnvArgs(env []string) []string {
	args := make([]string, 0, len(env))
	for _, variable := range env {
		args = append(args, "--env="+variable)
	}

	return args
}

// dockerPlainArgs renders an argument vector as command-line elements no shell will ever see.
func dockerPlainArgs(argv []string) []Arg {
	args := make([]Arg, 0, len(argv))
	for _, value := range argv {
		args = append(args, Arg{Value: value, Quote: false})
	}

	return args
}
