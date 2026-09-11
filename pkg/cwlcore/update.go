package cwlcore

import (
	"errors"
	"slices"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Version upgrade: v1.0 -> v1.1 -> v1.2, one step at a time.
// Validate against declared version first, then rewrite forwards.

// Supported CWL versions. V12 is the execution version; older docs are upgraded.
const (
	// CWLVersionV10 is CWL v1.0, the first stable release.
	CWLVersionV10 = "v1.0"
	// CWLVersionV11 is CWL v1.1.
	CWLVersionV11 = "v1.1"
)

// ErrUnsupportedVersion reports an unrecognized cwlVersion.
var ErrUnsupportedVersion = errors.New("unsupported cwlVersion")

// Extension namespaces used in v1.0 for features adopted in v1.1.
const (
	cwltoolNamespace  = "http://commonwl.org/cwltool#"
	arvadosNamespace  = "http://arvados.org/cwl#"
	cwltoolTimeLimit  = cwltoolNamespace + "TimeLimit"
	arvadosReuseClass = arvadosNamespace + "ReuseRequirement"
)

// v10ClassRenames maps v1.0 extension classes to their v1.1 core names.
var v10ClassRenames = map[string]string{
	cwltoolNamespace + ClassLoadListingRequirement:   ClassLoadListingRequirement,
	cwltoolNamespace + ClassInplaceUpdateRequirement: ClassInplaceUpdateRequirement,
	cwltoolNamespace + ClassNetworkAccess:            ClassNetworkAccess,
	cwltoolNamespace + ClassWorkReuse:                ClassWorkReuse,
	cwltoolTimeLimit:                                 ClassToolTimeLimit,
	arvadosReuseClass:                                ClassWorkReuse,
}

// upgradedClasses are the process classes whose requirements get v1.0->v1.1 rewrites.
var upgradedClasses = []string{ClassCommandLineTool, ClassWorkflow}

// inputBindingClasses lost CommandLineBinding fields on inputs in v1.1.
var inputBindingClasses = []string{ClassExpressionTool, ClassWorkflow}

// DeclaredVersion returns the cwlVersion from a raw parse, or "".
// Strips vocabulary prefixes (e.g. "cwl:v1.0" -> "v1.0").
func DeclaredVersion(root salad.Node) string {
	m, ok := salad.AsMap(root)
	if !ok {
		return ""
	}

	node, ok := m.Get(keyCWLVersion)
	if !ok {
		return ""
	}

	version, ok := salad.AsString(node)
	if !ok {
		return ""
	}

	return shortName(version)
}

// Upgrade rewrites a document from the given version to v1.2.
// Returns doc unchanged if already v1.2 or nil.
func Upgrade(doc *salad.Document, from string) *salad.Document {
	if doc == nil || (from != CWLVersionV10 && from != CWLVersionV11) {
		return doc
	}

	root := doc.Root
	if from == CWLVersionV10 {
		root = upgradeV10ToV11(root)
	}

	return &salad.Document{Root: upgradeV11ToV12(root), Metadata: doc.Metadata, BaseURI: doc.BaseURI}
}

// upgradeV10ToV11 applies the v1.0 -> v1.1 rewrites.
func upgradeV10ToV11(root salad.Node) salad.Node {
	out := visitClass(root, upgradedClasses, renameRequirements)
	out = visitClass(out, inputBindingClasses, trimInputBindings)
	out = visitField(out, keySecondaryFiles, secondaryFilePatterns)

	return withV10Defaults(out)
}

// upgradeV11ToV12 stamps every cwlVersion in the tree to v1.2.
func upgradeV11ToV12(root salad.Node) salad.Node {
	return visitField(root, keyCWLVersion, func(value salad.Node) salad.Node {
		return salad.NewStringNode(nodeLoc(value), CWLVersionV12)
	})
}

// renameRequirements rewrites extension classes in requirements, hints, and steps.
func renameRequirements(m *salad.MapNode) *salad.MapNode {
	out := renameClassesIn(m, keyRequirements)
	out = renameClassesIn(out, keyHints)

	return mapSeqField(out, keySteps, func(step salad.Node) salad.Node {
		nested, ok := salad.AsMap(step)
		if !ok {
			return step
		}

		return renameRequirements(nested)
	})
}

// renameClassesIn rewrites the class of every entry of one requirement list.
func renameClassesIn(m *salad.MapNode, key string) *salad.MapNode {
	return mapSeqField(m, key, func(item salad.Node) salad.Node {
		entry, ok := salad.AsMap(item)
		if !ok {
			return item
		}

		class, ok := salad.AsString(fieldNode(entry, keyClass))
		if !ok {
			return item
		}

		core, ok := v10ClassRenames[class]
		if !ok {
			return item
		}

		return entry.With(salad.MapEntry{Key: keyClass, Value: salad.NewStringNode(entry.Loc(), core)})
	})
}

// trimInputBindings strips inputBinding fields except loadContents.
func trimInputBindings(m *salad.MapNode) *salad.MapNode {
	return mapSeqField(m, keyInputs, func(item salad.Node) salad.Node {
		input, ok := salad.AsMap(item)
		if !ok {
			return item
		}

		binding, ok := salad.AsMap(fieldNode(input, keyInputBinding))
		if !ok {
			return item
		}

		kept := salad.NewMapNode(binding.Loc(), nil)
		if contents, has := binding.Get(keyLoadContents); has {
			kept = kept.With(salad.MapEntry{Key: keyLoadContents, Value: contents})
		}

		return input.With(salad.MapEntry{Key: keyInputBinding, Value: kept})
	})
}

// secondaryFilePatterns wraps v1.0 bare patterns into the v1.1 record form.
func secondaryFilePatterns(value salad.Node) salad.Node {
	seq, ok := salad.AsSeq(value)
	if !ok {
		if _, isMapping := salad.AsMap(value); isMapping {
			return value
		}

		return salad.NewSeqNode(nodeLoc(value), []salad.Node{patternRecord(value)})
	}

	items := make([]salad.Node, 0, seq.Len())
	for _, item := range seq.Items() {
		items = append(items, patternRecord(item))
	}

	return salad.NewSeqNode(seq.Loc(), items)
}

// patternRecord wraps a bare pattern in record form, passing mappings through.
func patternRecord(value salad.Node) salad.Node {
	if _, ok := salad.AsMap(value); ok {
		return value
	}

	return salad.NewMapNode(nodeLoc(value), []salad.MapEntry{{Key: keyPattern, Value: value}})
}

// withV10Defaults adds LoadListingRequirement(deep) and NetworkAccess(true) hints
// to preserve v1.0 implicit behavior after upgrade.
func withV10Defaults(root salad.Node) salad.Node {
	return mapTopLevelProcesses(root, func(m *salad.MapNode) *salad.MapNode {
		loc := m.Loc()
		hints := []salad.Node{
			requirementRecord(loc, ClassLoadListingRequirement, keyLoadListing,
				salad.NewStringNode(loc, string(LoadListingDeep))),
			requirementRecord(loc, ClassNetworkAccess, keyNetworkAccessField,
				salad.NewBoolNode(loc, true)),
		}

		if declared, ok := salad.AsSeq(fieldNode(m, keyHints)); ok {
			hints = append(hints, declared.Items()...)
		}

		return m.With(salad.MapEntry{Key: keyHints, Value: salad.NewSeqNode(loc, hints)})
	})
}

// requirementRecord builds a one-field requirement or hint object.
func requirementRecord(loc salad.SourceLine, class, key string, value salad.Node) salad.Node {
	return salad.NewMapNode(loc, []salad.MapEntry{
		{Key: keyClass, Value: salad.NewStringNode(loc, class)},
		{Key: key, Value: value},
	})
}

// mapTopLevelProcesses applies fn to each top-level process (root or graph members).
func mapTopLevelProcesses(root salad.Node, fn func(*salad.MapNode) *salad.MapNode) salad.Node {
	if seq, ok := salad.AsSeq(root); ok {
		return salad.NewSeqNode(seq.Loc(), mapNodes(seq.Items(), asMapping(fn)))
	}

	m, ok := salad.AsMap(root)
	if !ok {
		return root
	}

	if graph, isSeq := salad.AsSeq(fieldNode(m, keyGraph)); isSeq {
		return m.With(salad.MapEntry{
			Key:   keyGraph,
			Value: salad.NewSeqNode(graph.Loc(), mapNodes(graph.Items(), asMapping(fn))),
		})
	}

	return fn(m)
}

// asMapping lifts a MapNode rewrite into a Node rewrite.
func asMapping(fn func(*salad.MapNode) *salad.MapNode) func(salad.Node) salad.Node {
	return func(n salad.Node) salad.Node {
		m, ok := salad.AsMap(n)
		if !ok {
			return n
		}

		return fn(m)
	}
}

// visitClass applies fn to every mapping whose class matches, then descends.
func visitClass(n salad.Node, classes []string, fn func(*salad.MapNode) *salad.MapNode) salad.Node {
	recurse := func(child salad.Node) salad.Node { return visitClass(child, classes, fn) }

	m, ok := salad.AsMap(n)
	if !ok {
		return mapChildren(n, recurse)
	}

	if slices.Contains(classes, shortName(lenientText(m, keyClass))) {
		m = fn(m)
	}

	return mapChildren(m, recurse)
}

// visitField applies fn to every occurrence of the named field in the tree.
func visitField(n salad.Node, field string, fn func(salad.Node) salad.Node) salad.Node {
	recurse := func(child salad.Node) salad.Node { return visitField(child, field, fn) }

	m, ok := salad.AsMap(n)
	if !ok {
		return mapChildren(n, recurse)
	}

	if value, has := m.Get(field); has {
		m = m.With(salad.MapEntry{Key: field, Value: fn(value)})
	}

	return mapChildren(m, recurse)
}

// mapSeqField applies fn to every item of a sequence-valued field.
func mapSeqField(m *salad.MapNode, key string, fn func(salad.Node) salad.Node) *salad.MapNode {
	seq, ok := salad.AsSeq(fieldNode(m, key))
	if !ok {
		return m
	}

	return m.With(salad.MapEntry{Key: key, Value: salad.NewSeqNode(seq.Loc(), mapNodes(seq.Items(), fn))})
}

// mapChildren rebuilds a node with fn applied to each child.
// Rebuilds rather than mutates because resolved documents share nodes.
func mapChildren(n salad.Node, fn func(salad.Node) salad.Node) salad.Node {
	switch value := n.(type) {
	case *salad.MapNode:
		entries := make([]salad.MapEntry, 0, value.Len())
		for key, item := range value.All() {
			entries = append(entries, salad.MapEntry{Key: key, Value: fn(item)})
		}

		return salad.NewMapNode(value.Loc(), entries)
	case *salad.SeqNode:
		return salad.NewSeqNode(value.Loc(), mapNodes(value.Items(), fn))
	default:
		return n
	}
}

// mapNodes applies fn to each of nodes, returning a new slice.
func mapNodes(nodes []salad.Node, fn func(salad.Node) salad.Node) []salad.Node {
	out := make([]salad.Node, 0, len(nodes))
	for _, item := range nodes {
		out = append(out, fn(item))
	}

	return out
}
