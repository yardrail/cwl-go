package main

import (
	"fmt"
	"io"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// writeOutputs writes the run's output object to w as JSON.
func writeOutputs(w io.Writer, outputs map[string]any) error {
	encoded, err := cwlcli.JSON(outputObject(outputs))
	if err != nil {
		return fmt.Errorf("rendering the output object: %w", err)
	}

	fmt.Fprintln(w, string(encoded))

	return nil
}

// outputObject renders the output map in the CWL wire shape, keys sorted for determinism.
func outputObject(outputs map[string]any) *cwlcli.Object {
	object := cwlcli.NewObject()

	for _, name := range cwlcli.SortedKeys(outputs) {
		// Emit null explicitly; a missing key is not the same as null.
		value := outputs[name]
		if value == nil {
			object.Set(name, nil)

			continue
		}

		object.Set(name, cwlcore.ToExpressionValue(value))
	}

	return object
}
