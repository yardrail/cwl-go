package main

import "github.com/yardrail/cwl-go/cmd/internal/cwlcli"

// usageText returns the whole usage message.
//
// It is written out rather than assembled from [flag.PrintDefaults] because
// the exit statuses are this tool's real interface, and PrintDefaults has
// nowhere to put them.
func usageText() string {
	return `cwl-validate validates CWL documents against the embedded CWL schema for the
version each one declares -- v1.0, v1.1 or v1.2.

Usage:
  cwl-validate [flags] <document>...

Each document is a filesystem path or a URL, optionally with a #fragment
selecting one process of a $graph. Every document named is validated, so one
run reports every failure rather than costing a cycle per error.

Flags:
  -quiet, -q  print nothing; report the result through the exit status alone
  -strict     treat as errors the conditions the specification lets an
              implementation tolerate: a field the schema does not declare, and
              a requirement class this implementation does not recognize. The
              permissive default discards the first of those entirely, so a
              typo'd field name is reported nowhere without this
  -verbose,-v print every line of an error report; without it only the head of
              a long report is shown, because rejecting a value against an
              abstract type explains every concrete subtype it was tried as
  -version    print version information and exit

Exit status:
  0  every document is valid
  1  at least one document is not valid
  2  the command line could not be understood
`
}

// versionText returns the version banner.
func versionText() string {
	return cwlcli.VersionText(toolName)
}
