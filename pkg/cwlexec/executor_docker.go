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

const dockerEngine = "docker"

const dockerRun = "run"

// ErrContainerImage reports an image that could not be made available.
var ErrContainerImage = errors.New("container image is not available")

// dockerImages caches images already made available this process.
var dockerImages sync.Map

const dockerBuildPrefix = "cwl-docker-build-"

var _ ContainerExecutor = (*DockerCLIExecutor)(nil)

// DockerCLIExecutor shells out to the `docker` CLI. Zero value is usable.
type DockerCLIExecutor struct{}

// NewDockerCLIExecutor returns a new DockerCLIExecutor.
func NewDockerCLIExecutor() *DockerCLIExecutor { return &DockerCLIExecutor{} }

// EnsureImage makes the image available via docker pull/build/load/import.
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

// NewInvocation creates a Docker CLI invocation from the spec's mounts.
func (d *DockerCLIExecutor) NewInvocation(
	_ context.Context, ctr *ContainerSpec,
) (Invocation, error) {
	dirs := dockerInvocationDirs(ctr)

	return &dockerCLIInvocation{
		executor: d,
		ctr:      ctr,
		outfs:    NewLocalDirFS(dirs.outdir),
		stgfs:    NewLocalDirFS(dirs.stagingDir),
		tmpfs:    NewLocalDirFS(dirs.tmpdir),
	}, nil
}

// invocationDirs holds the host directories for a container invocation.
type invocationDirs struct {
	outdir     string
	tmpdir     string
	stagingDir string
}

// dockerInvocationDirs extracts outdir/tmpdir/staging from the first three mounts.
func dockerInvocationDirs(ctr *ContainerSpec) invocationDirs {
	if len(ctr.Mounts) >= containerWholeMounts {
		return invocationDirs{
			outdir:     ctr.Mounts[0].Source,
			tmpdir:     ctr.Mounts[1].Source,
			stagingDir: ctr.Mounts[2].Source,
		}
	}

	return invocationDirs{outdir: "", tmpdir: "", stagingDir: ""}
}

var _ Invocation = (*dockerCLIInvocation)(nil)

type dockerCLIInvocation struct {
	executor *DockerCLIExecutor
	ctr      *ContainerSpec
	outfs    *LocalDirFS
	stgfs    *LocalDirFS
	tmpfs    *LocalDirFS
}

func (i *dockerCLIInvocation) StageFS() WriteFS { return i.stgfs }
func (i *dockerCLIInvocation) OutFS() WriteFS   { return i.outfs }
func (i *dockerCLIInvocation) TmpFS() WriteFS   { return i.tmpfs }

func (i *dockerCLIInvocation) Run(ctx context.Context, spec *ProcessSpec) (int, error) {
	argv := i.executor.dockerArgv(i.ctr, spec)

	wrapped := *spec
	wrapped.Command = &CommandLine{Args: dockerPlainArgs(argv), Shell: false}
	wrapped.Env = os.Environ()

	return RunProcess(ctx, &wrapped)
}

func (i *dockerCLIInvocation) Close() error {
	return nil
}

// fetch acquires the image by the applicable method.
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

// present checks whether the image exists locally.
func (d *DockerCLIExecutor) present(ctx context.Context, image string) bool {
	return d.engine(ctx, "inspect", image) == nil
}

// build builds an image from a literal Dockerfile.
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

// load loads a saved image archive.
func (d *DockerCLIExecutor) load(ctx context.Context, source string) error {
	local, err := dockerLocalArchive(source)
	if err != nil {
		return err
	}

	return d.engine(ctx, "load", "--input", local)
}

// engine runs a docker subcommand.
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

// dockerArgv builds the `docker run` argument vector.
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

// dockerLocalArchive resolves a dockerLoad source to a local path.
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

const dockerSchemeSeparator = "://"

// dockerMountArgs renders mounts as --mount flags.
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

// dockerMountArg renders one bind mount as --mount=type=bind,...
func dockerMountArg(source, target string, mode ...string) string {
	options := append([]string{"type=bind", "source=" + source, "target=" + target}, mode...)

	quoted := make([]string, 0, len(options))
	for _, option := range options {
		quoted = append(quoted, dockerCSVField(option))
	}

	return "--mount=" + strings.Join(quoted, ",")
}

// dockerCSVField quotes a CSV field if it contains commas or quotes.
func dockerCSVField(field string) string {
	if !strings.ContainsAny(field, `,"`) {
		return field
	}

	return `"` + strings.ReplaceAll(field, `"`, `""`) + `"`
}

var dockerNetworkModes = map[bool][]string{true: nil, false: {"--net=none"}}

var dockerReadOnlyModes = map[bool][]string{true: {"--read-only=true"}, false: nil}

var dockerRemoveModes = map[bool][]string{true: {"--rm"}, false: nil}

// dockerLogArgs disables Docker log capture when stdout is being captured to a file.
func dockerLogArgs(stdout string) []string {
	if stdout == "" {
		return nil
	}

	return []string{"--log-driver=none"}
}

// dockerUserArgs adds --user matching this process's uid:gid.
func dockerUserArgs(ctr *ContainerSpec) []string {
	if !ctr.MatchUser {
		return nil
	}

	return []string{fmt.Sprintf("--user=%d:%d", os.Geteuid(), os.Getgid())}
}

// dockerEnvArgs renders environment variables as --env flags.
func dockerEnvArgs(env []string) []string {
	args := make([]string, 0, len(env))
	for _, variable := range env {
		args = append(args, "--env="+variable)
	}

	return args
}

// dockerPlainArgs converts strings to unquoted Args.
func dockerPlainArgs(argv []string) []Arg {
	args := make([]Arg, 0, len(argv))
	for _, value := range argv {
		args = append(args, Arg{Value: value, Quote: false})
	}

	return args
}
