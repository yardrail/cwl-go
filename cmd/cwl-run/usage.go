package main

import "github.com/yardrail/cwl-go/cmd/internal/cwlcli"

// usageText returns the whole usage message.
//
// It is written out rather than assembled from [flag.PrintDefaults] because the
// exit statuses are this tool's real interface — the cwltest harness reads
// nothing else — and PrintDefaults has nowhere to put them.
func usageText() string {
	return `cwl-run executes a CWL document and prints its output object as JSON. Documents
declaring v1.0 or v1.1 are validated against their own version's schema and
upgraded before they run.

Usage:
  cwl-run [flags] <process> [<job>]

<process> is a filesystem path or a URL, optionally with a #fragment selecting
one process of a $graph. <job> is a job order in YAML or JSON supplying the
input object; without one the process runs against an empty input object, which
succeeds only if every input is optional or has a default.

Stdout carries the output object and nothing else, so it can be piped into a
comparison. Every log line, warning and error goes to stderr.

Flags:
  -outdir DIR  write the run's output files under DIR, which is created if it
               does not exist. The default is the process working directory
  -quiet, -q   suppress progress and advisory messages on stderr; failures are
               still reported, because a silent failure is not a result
  -verbose,-v  print every line of an error report; without it only the head of
               a long report is shown, because rejecting a value against an
               abstract type explains every concrete subtype it was tried as
  -version     print version information and exit

Software container flags. They are cwltool's, name for name, and each is an
opt-out: without them a DockerRequirement runs a container, the tool runs as
this process's user, the container's root filesystem is read-only, and the
container is removed when the tool exits. They apply to the whole run, nested
subworkflows included.

  -no-container      do not run tools in a software container. A
                     DockerRequirement in hints is declined and the tool runs
                     on this host, which is what "an implementation may ignore
                     a hint" permits. One in requirements is refused instead,
                     and exits 33: the document says the tool must run in that
                     image, and running it anywhere else is a different answer
                     rather than a lesser one
  -no-match-user     do not pass this process's uid and gid to the container.
                     Without it the tool runs as the image's user, usually
                     root, and what it writes into the output directory is
                     owned by root on this host
  -no-read-only      do not hold the container's root filesystem read-only, so
                     a tool may write outside the directories staged for it
  -leave-container   do not remove the container once its tool has exited, so
                     that it can be inspected. Nothing else collects it

Exit status:
  0   the run succeeded and its output object is on stdout
  1   the run did not produce an output object: the document is invalid, the
      job order does not fit it, or a step failed
  2   the command line could not be understood
  33  the document uses a feature this engine does not implement, including a
      cwlVersion there is no vendored schema for. v1.0, v1.1 and v1.2 all run;
      anything else stops here. It is also what -no-container reports for a
      DockerRequirement under requirements. Nothing was executed. The cwltest
      harness counts this as a skipped test rather than a failed one
`
}

// versionText returns the version banner.
func versionText() string {
	return cwlcli.VersionText(toolName)
}
