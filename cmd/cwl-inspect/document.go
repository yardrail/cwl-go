package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// documentObject dumps a resolved salad document: base URI, metadata, and resolved tree.
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

// graphObject dumps every top-level process in the document, sorted by id.
func graphObject(doc *salad.Document, processes map[string]cwlcore.Process) *cwlcli.Object {
	o := cwlcli.NewObject()
	if doc != nil {
		o.SetString("baseURI", doc.BaseURI)
	}

	ids := cwlcli.SortedKeys(processes)
	items := make([]any, 0, len(ids))

	for _, id := range ids {
		// Skip broken decodes rather than panicking.
		process := processes[id]
		if process == nil {
			continue
		}

		items = append(items, processObject(process))
	}

	return o.Set("processes", items)
}
