package cwlexec

import (
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// The input object's optional requirements merge.
//
// concepts.md: "Optionally, implementations may allow requirements to be specified in the input
// object document as an array of requirements under the field name `cwl:requirements`. If
// implementations allow this, then such requirements should be combined with any requirements
// present in the corresponding Process as if they were specified there."
//
// "As if they were specified there" leaves the tie unbroken, and a job order that supplies a
// requirement the process already declares has to resolve one way or the other. The conformance
// suite settles it: cwl_requirements_override_static runs tests/env-tool3.cwl, which declares no
// EnvVarRequirement, against tests/env-job4.yaml, which supplies one, and expects the supplied
// value; cwl_requirements_override_expression runs tests/env-tool4.cwl, which *does* declare
// `TEST_ENV: conflict_original`, against tests/env-job3.yaml, which supplies
// `TEST_ENV: $(inputs.in)`, and expects the job order's expression to have won. So the input
// object's declarations are appended after the process's own, where the last-declaration-wins rule
// [cwlcore.RequirementScope] already implements makes them the effective ones.
//
// Appending to the process rather than resolving the merge separately is what carries the
// requirement everywhere it has to reach: a nested workflow step inherits it through the ordinary
// scope chain, subject to the same inheritance-validity filter as any other requirement a top-level
// process declares, and nothing downstream needs to know the declaration came from a job order.

// joKeyRequirements is the input-object field that carries requirements to merge into the process's
// own. The prefixed spelling is the one the specification names, and it is also what keeps the
// field out of the undeclared-key warning: [joReservedKey] exempts any key containing a colon.
const joKeyRequirements = "cwl:requirements"

// joKeyProcessRequirements is the field name a process document writes the same list under, and so
// the key the synthetic carrier node in [joMergeRequirements] has to use.
const joKeyProcessRequirements = "requirements"

// joMergeRequirements appends the requirements the input object supplies to p's own.
//
// The class discriminator is the only thing the synthetic node needs beyond the requirements list:
// decoding a requirements field is the same work whatever process declared it, and an Operation is
// the cheapest process to decode, having neither a command line nor steps to walk. An entry naming
// a class this engine does not model decodes into a [cwlcore.RawRequirement] exactly as it would in
// a process document, and is rejected later by the same capability check, rather than being
// silently dropped here.
//
// Loading two job orders against one process appends twice, which changes nothing about what is in
// effect — the last declaration of a class wins either way — but does leave the earlier, shadowed
// entry on the list. A process is loaded for one run, so that does not arise in practice.
func joMergeRequirements(root salad.Node, p cwlcore.Process) error {
	object, ok := salad.AsMap(root)
	if !ok {
		// Not a mapping at all, which [joLoader.load] reports against the whole document.
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
