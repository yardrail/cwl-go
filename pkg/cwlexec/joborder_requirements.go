package cwlexec

import (
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Merging cwl:requirements from the input object into the process's requirements.
// Appended after the process's own so last-declaration-wins applies.

// joKeyRequirements is the input-object field for merged requirements.
const joKeyRequirements = "cwl:requirements"

// joKeyProcessRequirements is the field name for requirements in a process document.
const joKeyProcessRequirements = "requirements"

// joMergeRequirements appends cwl:requirements from the input object to p's requirements.
func joMergeRequirements(root salad.Node, p cwlcore.Process) error {
	object, ok := salad.AsMap(root)
	if !ok {
		return nil
	}

	node, ok := object.Get(joKeyRequirements)
	if !ok || salad.IsNull(node) {
		return nil
	}

	loc := node.Loc()
	synthetic := salad.NewMapNode(loc, []salad.MapEntry{
		{Key: outKeyClass, Value: salad.NewStringNode(loc, cwlcore.ClassOperation)},
		{Key: joKeyProcessRequirements, Value: node},
	})

	carrier, err := cwlcore.DecodeNode(synthetic)
	if err != nil {
		return err
	}

	base := p.Base()
	base.Requirements = slices.Concat(base.Requirements, carrier.Base().Requirements)

	return nil
}
