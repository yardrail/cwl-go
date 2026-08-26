package main

import (
	"fmt"
	"io"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// writeOutputs writes the run's output object to w as JSON, and nothing else.
//
// This is the whole of stdout. cwltest parses it in full and compares it
// structurally against the expected object — Any is a wildcard, a File's
// checksum and size are re-verified against the file on disk, a location is
// matched by suffix, and a key the expected object does not have is an error
// except inside a File or a Directory. So a progress line, a warning, or a
// banner printed here does not merely look untidy: it fails a test that
// otherwise passed. Everything else this tool says goes to stderr.
func writeOutputs(w io.Writer, outputs map[string]any) error {
	encoded, err := cwlcli.JSON(outputObject(outputs))
	if err != nil {
		return fmt.Errorf("rendering the output object: %w", err)
	}

	fmt.Fprintln(w, string(encoded))

	return nil
}

// outputObject renders the engine's output map in the CWL wire shape, with a
// fixed key order.
//
// The values arrive typed — a File is a [*cwlcore.File], not a map — and
// [cwlcore.ToExpressionValue] is what turns them back into the objects a CWL
// document writes and a JSON comparison expects. It is used rather than a
// converter of this tool's own precisely because it already draws the one
// distinction that is easy to get wrong and impossible to see afterwards:
// absent is not zero. An unmeasured size is omitted while an explicit 0 is
// emitted, and a nil directory listing — which means "not read", not "empty" —
// is omitted rather than written as [].
//
// Keys are sorted so that two runs of the same document produce byte-identical
// output. The engine hands them over in a Go map, whose iteration order is
// random, so this is the only place the order can be pinned.
func outputObject(outputs map[string]any) *cwlcli.Object {
	object := cwlcli.NewObject()

	for _, name := range cwlcli.SortedKeys(outputs) {
		// A null output — an unwired workflow output, or one a
		// skipped step never produced — is emitted as null rather than
		// dropped: the specification gives every declared output
		// parameter a value, and a missing key is not the same answer
		// as an explicit null.
		value := outputs[name]
		if value == nil {
			object.Set(name, nil)

			continue
		}

		object.Set(name, cwlcore.ToExpressionValue(value))
	}

	return object
}
