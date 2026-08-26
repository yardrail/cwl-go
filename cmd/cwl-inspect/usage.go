package main

// usageText returns the whole usage message.
//
// The stages are described rather than listed, because which one to reach for
// is the only decision this tool asks its user to make and the names alone do
// not make it.
func usageText() string {
	return `cwl-inspect dumps the intermediate representation of a CWL v1.2 document,
for debugging pkg/salad and pkg/cwlcore.

Usage:
  cwl-inspect [flags] <document>

The document is a filesystem path or a URL, optionally with a #fragment
selecting one process of a $graph. The dump goes to stdout and any diagnostic
to stderr, and it is deterministic: two runs over the same input produce
byte-identical output, so runs can be diffed against each other.

Flags:
  -stage parsed|resolved|typed|graph|scope   which representation to dump
                                             (default typed)
  -format json|text                          output encoding (default json)
  -version                                   print version information and exit

Stages, in the order a document passes through them:
  parsed    the salad node tree as the YAML parser produced it, before any
            resolution, with the source line and kind of every node. It is the
            only stage that needs no schema, so it is what you look at when the
            document will not load at all.
  resolved  the same tree after the loader has run: $import and $include
            spliced in, identifiers made absolute, vocabulary terms expanded,
            and the typedsl and idmap shorthands rewritten into their long
            forms. Diff it against parsed to see what resolution changed.
  typed     the model pkg/cwlcore decoded from that tree: the process, its
            parameters, its steps, its requirements. Reach for it when the
            document loads but does not execute as written.
  graph     every top-level process the document declares, not only the one the
            entry-point rules select. It is how you see the rest of a $graph.
  scope     the requirements and hints in effect for the process, and for each
            step of a workflow, with where each came from and what the
            fail-closed gate makes of them. Reach for it when a requirement is
            not reaching the tool that needs it.

Exit status:
  0  the dump was produced
  1  the document could not be read, loaded, validated or decoded
  2  the command line could not be understood
`
}
