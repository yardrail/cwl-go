package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// documentObject dumps a resolved salad document: the base URI everything
// resolved against, the metadata directives, and the whole resolved tree.
//
// This is the view that answers "what did the loader actually do", and it is a
// different question from what the parser produced. Between the two, $import
// and $include are spliced in, identifiers become absolute, vocabulary terms
// expand to IRIs and the schema's typedsl and idmap shorthands are rewritten
// into their long forms. Every one of those is a place a document can stop
// meaning what its author wrote, and none of them is visible in either the
// parse tree or the typed model.
func documentObject(doc *salad.Document) *cwlcli.Object {
	o := cwlcli.NewObject()
	if doc == nil {
		return o
	}

	o.SetString("baseURI", doc.BaseURI)

	if doc.Metadata != nil {
		o.Set("metadata", nodeObject(doc.Metadata))
	}

	return o.Set("root", nodeObject(doc.Root))
}

// graphObject dumps every top-level process a document declares.
//
// A $graph document holds several processes and the entry-point rules pick one
// of them, so the typed stage necessarily hides the rest. This shows all of
// them, sorted by identifier so that two runs — and two documents — compare.
// A document with no $graph yields the single entry for its root process,
// which is what makes this safe to reach for without knowing which kind of
// document is in front of you.
func graphObject(doc *salad.Document, processes map[string]cwlcore.Process) *cwlcli.Object {
	o := cwlcli.NewObject()
	if doc != nil {
		o.SetString("baseURI", doc.BaseURI)
	}

	ids := cwlcli.SortedKeys(processes)
	items := make([]any, 0, len(ids))

	for _, id := range ids {
		// A nil value would be a broken decode rather than a document
		// with nothing in it. Skipping beats dereferencing: a dump that
		// is one process short is still a dump, and a panic is not.
		process := processes[id]
		if process == nil {
			continue
		}

		items = append(items, processObject(process))
	}

	return o.Set("processes", items)
}
