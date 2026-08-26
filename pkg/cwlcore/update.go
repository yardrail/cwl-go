package cwlcore

import (
	"errors"
	"slices"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Upgrading a document written against an earlier CWL version into the v1.2 form
// the rest of this package decodes.
//
// The shape of the pipeline is cwltool's, in resolve_and_validate_document: read
// the declared version off the raw parse, validate the document against that
// version's schema and nothing else, then rewrite the validated tree forwards
// one version at a time. Validating first and upgrading second is what makes
// "this document uses syntax its declared version did not have" reportable at
// all — the checks that catch it are precisely the ones an upgrade removes.
//
// The rewrites below are cwltool's update.v1_0to1_1 and update.v1_1to1_2. They
// were re-derived from a field-by-field diff of the three vendored schemas
// rather than copied on trust, and where the two disagree the divergence is
// noted at the rule it affects.

// The CWL versions this implementation can read. CWLVersionV12 is the one it
// executes; a document declaring either of the others is validated against its
// own schema and then upgraded, so nothing downstream of Load ever sees one.
const (
	// CWLVersionV10 is CWL v1.0, the first stable release.
	CWLVersionV10 = "v1.0"
	// CWLVersionV11 is CWL v1.1.
	CWLVersionV11 = "v1.1"
)

// ErrUnsupportedVersion reports a document declaring a cwlVersion this
// implementation has no schema for — a draft release, or a development version.
//
// It is deliberately not the same thing as an invalid document. A caller that
// distinguishes the two can say "this engine cannot read that version" instead of
// "that document is malformed", which for the cwl-runner exit-code contract is
// the difference between an unsupported feature and a failure.
var ErrUnsupportedVersion = errors.New("unsupported cwlVersion")

// The extension namespaces a v1.0 document had to reach into for features CWL
// v1.1 later adopted. They are spelled in full because that is how they arrive:
// a resolved document carries an extension class as its absolute IRI, since no
// vocabulary term maps it back to a short name.
const (
	cwltoolNamespace  = "http://commonwl.org/cwltool#"
	arvadosNamespace  = "http://arvados.org/cwl#"
	cwltoolTimeLimit  = cwltoolNamespace + "TimeLimit"
	arvadosReuseClass = arvadosNamespace + "ReuseRequirement"
)

// v10ClassRenames maps the extension requirement classes a v1.0 document had to
// use onto the core classes CWL v1.1 gave them.
//
// The set is not a guess: diffing the vendored v1.0 and v1.1 schemas shows
// exactly five requirement records added in v1.1 — LoadListingRequirement,
// InplaceUpdateRequirement, NetworkAccess, ToolTimeLimit and WorkReuse — and
// those five are precisely the ones that existed beforehand as cwltool
// extensions. Note the one name that changed on adoption: cwltool spelled it
// TimeLimit, and CWL adopted it as ToolTimeLimit.
//
// The Arvados entry is the one rule the schemas cannot derive; it is carried
// because cwltool's update.v1_0to1_1 carries it, and a document written for
// Arvados is otherwise silently stripped of its reuse policy.
var v10ClassRenames = map[string]string{
	cwltoolNamespace + ClassLoadListingRequirement:   ClassLoadListingRequirement,
	cwltoolNamespace + ClassInplaceUpdateRequirement: ClassInplaceUpdateRequirement,
	cwltoolNamespace + ClassNetworkAccess:            ClassNetworkAccess,
	cwltoolNamespace + ClassWorkReuse:                ClassWorkReuse,
	cwltoolTimeLimit:                                 ClassToolTimeLimit,
	arvadosReuseClass:                                ClassWorkReuse,
}

// upgradedClasses are the process classes cwltool's v1.0 updater rewrites the
// requirements of. It is cwltool's set exactly, which notably leaves an
// ExpressionTool's own requirements alone; the two classes below are the only
// ones a v1.0 document could have hung a cwltool extension off in practice.
var upgradedClasses = []string{ClassCommandLineTool, ClassWorkflow}

// inputBindingClasses are the process classes whose inputs lost every
// CommandLineBinding field in v1.1.
//
// v1.0 gave InputParameter an inputBinding of type InputBinding, which is
// abstract and whose only concrete subtype is CommandLineBinding — so a Workflow
// or ExpressionTool input could carry a position, a prefix, the lot, none of
// which means anything off a command line. v1.1 removed the field and moved
// loadContents onto the parameter itself. See cwltool's fix_inputBinding.
var inputBindingClasses = []string{ClassExpressionTool, ClassWorkflow}

// DeclaredVersion returns the cwlVersion a document declares, or "" when it
// declares none.
//
// It reads a raw parse rather than a resolved document, which is the whole point
// of it: a v1.0 document is not required to satisfy the v1.2 schema, so resolving
// or validating one first would report it as invalid when the truthful answer is
// that it is a valid document of an earlier version. Reading the field off an
// unvalidated tree is what lets the version answer come first, and it is what
// [LoadDocument] routes on.
//
// The vocabulary prefixes are stripped, so "cwl:v1.0" and
// "https://w3id.org/cwl/cwl#v1.0" both answer "v1.0" — the same normalization
// cwltool applies in resolve_and_validate_document. A root that is not a mapping,
// or whose cwlVersion is not a string, declares nothing.
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

// Upgrade rewrites a resolved document declaring version from into the v1.2 form
// this package decodes, returning doc unchanged when there is nothing to do.
//
// The chain runs one version at a time — v1.0 to v1.1, then v1.1 to v1.2 — so a
// rule only ever has to describe one release's worth of change, which is how
// cwltool's ORDERED_VERSIONS chain is built and why adding v1.3 later is a new
// link rather than a new matrix.
//
// It takes a version rather than reading one off the document because by the time
// it runs the version may no longer be there to read: a $graph document declares
// it once, alongside the graph, and resolution has already replaced the mapping
// that held it with the sequence it held. Pass what [DeclaredVersion] returned.
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

// upgradeV10ToV11 applies the four rewrites cwltool's v1_0to1_1 applies to the
// document body, in its order.
func upgradeV10ToV11(root salad.Node) salad.Node {
	out := visitClass(root, upgradedClasses, renameRequirements)
	out = visitClass(out, inputBindingClasses, trimInputBindings)
	out = visitField(out, keySecondaryFiles, secondaryFilePatterns)

	return withV10Defaults(out)
}

// upgradeV11ToV12 is the whole of cwltool's v1_1to1_2: the version stamp, and
// nothing else. Diffing the two schemas confirms it — v1.2 only widens (a
// fractional ResourceRequirement, a step's when, pickValue, Operation), and a
// widening needs no rewrite because everything the old version accepted the new
// one still accepts.
//
// Where this differs from cwltool is which nodes it stamps. cwltool deletes
// cwlVersion from each top-level process and writes the new version once onto the
// document root; this model has no document root to write it on — every process
// carries its own version — so every cwlVersion in the tree is rewritten instead,
// nested inline run: targets included. The invariant that buys is worth the
// divergence: after Upgrade, no node in the document claims a version this engine
// does not run, so nothing downstream has to ask again.
func upgradeV11ToV12(root salad.Node) salad.Node {
	return visitField(root, keyCWLVersion, func(value salad.Node) salad.Node {
		return salad.NewStringNode(nodeLoc(value), CWLVersionV12)
	})
}

// renameRequirements rewrites the extension classes in one process's
// requirements and hints, and in those of every step it declares.
//
// The descent into steps is explicit because a step is not a process: it has no
// class of its own, so a walk that dispatches on class alone would never reach
// the requirements a step declares. This mirrors cwltool's rewrite_requirements,
// which recurses the same way and for the same reason.
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

// trimInputBindings strips every field but loadContents from the inputBinding of
// each of a process's inputs.
//
// The whole binding is not simply dropped, because loadContents survived the
// move: v1.1 kept it on InputBinding and deprecated it there rather than
// removing it, so a v1.0 document that asked for the file's contents still gets
// them. Everything else described a command line the process does not have.
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

// secondaryFilePatterns rewrites a v1.0 secondaryFiles value into the record form
// v1.1 introduced: a bare pattern becomes {pattern: <it>}, and a lone pattern
// becomes a one-element list of one.
//
// A trailing "?" is deliberately left in the pattern rather than read as "this
// file is optional". That reading is a v1.1 addition, so in a v1.0 document the
// character is part of the name — which is why this cannot reuse pkg/salad's
// secondaryFilesDSL expansion, whose whole job is to apply the newer rule. It is
// also what cwltool's update_secondaryFiles does.
//
// Values that are already mappings are left alone. That is what keeps a File
// object's own secondaryFiles — a list of File and Directory objects, not
// patterns — from being wrapped in a record it has no business being in.
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

// patternRecord wraps one bare pattern in the record form, passing a value that
// is already a mapping through untouched.
func patternRecord(value salad.Node) salad.Node {
	if _, ok := salad.AsMap(value); ok {
		return value
	}

	return salad.NewMapNode(nodeLoc(value), []salad.MapEntry{{Key: keyPattern, Value: value}})
}

// withV10Defaults prepends to every top-level process the two hints that spell
// out what v1.0 did implicitly.
//
// Both are behaviour-preserving rather than cosmetic. v1.1 introduced
// LoadListingEnum with a default of no_listing, where v1.0 always loaded a
// Directory's listing in full; and it introduced NetworkAccess, defaulting to no
// network, where v1.0 placed no restriction. Upgrading a document without these
// would quietly change what it does, which is the one thing an upgrade must not
// do. They go in as hints rather than requirements so that a document that says
// otherwise for itself still wins.
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

// mapTopLevelProcesses applies fn to each process a document declares at its top
// level, which is either the root itself or every member of its graph.
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

// asMapping lifts a mapping rewrite into a node rewrite that passes anything else
// through.
func asMapping(fn func(*salad.MapNode) *salad.MapNode) func(salad.Node) salad.Node {
	return func(n salad.Node) salad.Node {
		m, ok := salad.AsMap(n)
		if !ok {
			return n
		}

		return fn(m)
	}
}

// visitClass rebuilds n with fn applied to every mapping whose class is one of
// classes. It is the analogue of cwltool's visit_class.
//
// fn runs before the descent, so a rewrite that reaches into a mapping's children
// — renameRequirements reaching into steps — is not undone by the walk that
// follows it.
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

// visitField rebuilds n with fn applied to the value of every field named field,
// wherever in the tree it occurs. It is the analogue of cwltool's visit_field.
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

// mapSeqField applies fn to every item of one sequence-valued field, leaving a
// mapping whose field is absent or is not a sequence untouched.
func mapSeqField(m *salad.MapNode, key string, fn func(salad.Node) salad.Node) *salad.MapNode {
	seq, ok := salad.AsSeq(fieldNode(m, key))
	if !ok {
		return m
	}

	return m.With(salad.MapEntry{Key: key, Value: salad.NewSeqNode(seq.Loc(), mapNodes(seq.Items(), fn))})
}

// mapChildren rebuilds one node with fn applied to each of its children,
// returning a scalar unchanged.
//
// Rebuilding rather than mutating is not a style choice: a resolved document is
// shared — pkg/salad splices one parsed $import into every place that imported it
// — so writing through a node would change documents the caller never asked about.
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
