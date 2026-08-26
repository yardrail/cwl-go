package main

import "github.com/yardrail/cwl-go/cmd/internal/cwlcli"

// usageText returns the whole usage message.
//
// It is written out rather than assembled from [flag.PrintDefaults] because the
// exit statuses are this tool's real interface — the cwltest harness reads
// nothing else — and PrintDefaults has nowhere to put them.
func usageText() string {
	return `cwl-run executes a CWL v1.2 document and prints its output object as JSON.

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

Exit status:
  0   the run succeeded and its output object is on stdout
  1   the run did not produce an output object: the document is invalid, the
      job order does not fit it, or a step failed
  2   the command line could not be understood
  33  the document uses a feature this engine does not implement, including a
      cwlVersion other than v1.2. Nothing was executed. The cwltest harness
      counts this as a skipped test rather than a failed one
`
}

// versionText returns the version banner.
func versionText() string {
	return cwlcli.VersionText(toolName)
}
