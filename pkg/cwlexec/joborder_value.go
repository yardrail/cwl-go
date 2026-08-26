package cwlexec

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Type-checking and conversion of a single job-order value.
//
// The strictness here is deliberately calibrated: strict enough that a value of the wrong shape
// is caught at load time with a source line, loose enough that it never rejects a job file a
// conforming runner should accept.
//
// What is checked:
//
//   - null is accepted only where the declared type is a union containing null (which is what
//     `T?` expands to) or the null primitive itself.
//   - boolean, string, int, long, float and double each require a scalar of that kind. An
//     integer widens to float and double, because every integer is a real number and neither
//     YAML nor JSON can write 1.0 as distinct from 1 once parsed; a float does not narrow to int
//     or long, following the same reasoning as salad's own validator, which is that YAML and
//     JSON do distinguish 3 from 3.0 and silent narrowing hides real mistakes.
//   - File and Directory require a mapping whose `class` says so, and their own fields are
//     checked: an unknown field is an error, `contents` must be a string within the
//     specification's 64 KiB ceiling, and location, path, basename and format must be strings.
//   - An array requires a sequence, and each element is checked against the item type. A single
//     value is not accepted where an array is declared.
//   - A record requires a mapping. Every declared field is checked, a missing field whose type
//     does not accept null is an error, and an undeclared field is an error.
//   - An enum requires a string matching one of its symbols, compared by short name.
//   - A union is satisfied if any member accepts the value; when none does, the error carries
//     one child per member explaining why.
//   - Any accepts any non-null value, per the schema's own definition of the wildcard.
//
// What is not checked:
//
//   - int is not range-checked against 32 bits. The engine carries int and long alike as an
//     int64 and the distinction has no effect downstream, so a range check would reject values
//     that go on to behave correctly.
//   - A named type, one declared by a SchemaDefRequirement, is accepted as it stands. Resolving
//     the name needs the requirement scope, including requirements inherited from an enclosing
//     workflow and its steps, which is the scheduler's to assemble; treating an unresolvable
//     name as an error here would reject valid documents, and guessing at a resolution would be
//     worse than not checking at all.
//   - `format` is checked only where the process document names no $schemas ontology, since the
//     specification licenses exact matching only in that case; see [joLoader.checkFormat]. An
//     expression-valued declared format is never checked here, because evaluating it needs the
//     completed input object.
//
// Values not covered by a declared type, meaning the payload of an `Any` and the undeclared
// corners of a mapping, are still walked, and any mapping inside them carrying `class: File` or
// `class: Directory` is normalised into the typed value. The specification requires that "all
// files listed in the input object must be made available in the runtime", and does not qualify
// that by how the containing parameter happened to be declared.

// joValueCtx is everything conversion needs about the position it is converting at: the type the
// value must satisfy, the directory relative references resolve against, a dotted breadcrumb for
// diagnostics, and whether a File here should have its contents read.
//
// It is passed by pointer and copied explicitly by the three descent helpers, so that a nested
// position can adjust one field without disturbing its parent.
type joValueCtx struct {
	typ          cwlcore.TypeRef
	base         string
	path         string
	format       []string
	listing      cwlcore.LoadListingEnum
	loadContents bool
}

// withType returns a copy of v expecting typ at the same position, for descending into a union
// member.
func (v *joValueCtx) withType(typ cwlcore.TypeRef) *joValueCtx {
	next := *v
	next.typ = typ

	return &next
}

// at returns a copy of v for a nested position, expecting typ.
//
// The three settings a declaration carries — loadContents, loadListing and format — do not
// descend. The specification scopes each of them to the value bound to the declaration itself:
// loadContents to "type: File or an array of items: File", loadListing to a Directory bound to
// the parameter, and format to the value bound to the parameter and explicitly not to its
// secondary files. A nested position that has a declaration of its own sets them again from it;
// [joLoader.field] is the one that does. item carries them forward for the one case where they do
// apply unchanged.
func (v *joValueCtx) at(step string, typ cwlcore.TypeRef) *joValueCtx {
	next := *v
	next.typ = typ
	next.path += step
	next.loadContents = false
	next.listing = ""
	next.format = nil

	return &next
}

// item returns a copy of v for element i of an array of typ, keeping loadContents, loadListing
// and format, which the specification applies to an array element by element.
func (v *joValueCtx) item(i int, typ cwlcore.TypeRef) *joValueCtx {
	next := *v
	next.typ = typ
	next.path += fmt.Sprintf("[%d]", i)

	return &next
}

// value converts and checks one node against v's declared type.
func (l *joLoader) value(ctx context.Context, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	switch v.typ.Kind() {
	case cwlcore.TypeKindUnion:
		return l.union(ctx, n, v)
	case cwlcore.TypeKindArray:
		return l.array(ctx, n, v)
	case cwlcore.TypeKindRecord:
		return l.record(ctx, n, v)
	case cwlcore.TypeKindEnum:
		return joEnumValue(n, v)
	case cwlcore.TypeKindPrimitive:
		return l.primitive(ctx, n, v)
	case cwlcore.TypeKindStdin:
		// The `stdin` shortcut declares a File wired to standard input; as an input value
		// it is an ordinary File.
		return l.fileValue(ctx, n, v)
	default:
		// TypeKindNamed and TypeKindUnset, plus the stdout and stderr shortcuts, which are
		// output-only and so never reach a job order. Nothing to check; the value is still
		// walked so that File and Directory objects inside it are normalised.
		return l.freeform(ctx, n, v)
	}
}

// primitive converts a value against a CWLType symbol.
func (l *joLoader) primitive(ctx context.Context, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	switch v.typ.Name() {
	case cwlcore.PrimitiveNull:
		if !salad.IsNull(n) {
			return nil, joTypeErr(n, v)
		}

		return nil, nil
	case cwlcore.PrimitiveFile:
		return l.fileValue(ctx, n, v)
	case cwlcore.PrimitiveDirectory:
		return l.directoryValue(ctx, n, v)
	case cwlcore.PrimitiveAny:
		// Process.yml gives Any as "a wildcard for any non-null value".
		if salad.IsNull(n) {
			return nil, joTypeErr(n, v)
		}

		return l.freeform(ctx, n, v)
	default:
		return joScalarValue(n, v)
	}
}

// joScalarValue converts a boolean, a string or a numeric primitive.
func joScalarValue(n salad.Node, v *joValueCtx) (any, *salad.Error) {
	scalar, ok := salad.AsScalar(n)
	if !ok {
		return nil, joTypeErr(n, v)
	}

	switch v.typ.Name() {
	case cwlcore.PrimitiveBoolean:
		if !scalar.IsBool() {
			return nil, joTypeErr(n, v)
		}

		return scalar.AsBool(), nil
	case cwlcore.PrimitiveInt, cwlcore.PrimitiveLong:
		return joIntValue(scalar, n, v)
	case cwlcore.PrimitiveFloat, cwlcore.PrimitiveDouble:
		return joFloatValue(scalar, n, v)
	case cwlcore.PrimitiveString:
		text, isText := scalar.AsString()
		if !isText {
			return nil, joTypeErr(n, v)
		}

		return text, nil
	default:
		return nil, salad.Errorf(joNodeLoc(n), "%s: %q is not a CWL type", v.path, v.typ.Name())
	}
}

// joIntValue converts an int or a long. A float is rejected even when it holds a whole number.
func joIntValue(scalar *salad.ScalarNode, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	number, ok := scalar.AsInt()
	if !ok {
		return nil, joTypeErr(n, v)
	}

	return number, nil
}

// joFloatValue converts a float or a double. An integer widens.
func joFloatValue(scalar *salad.ScalarNode, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	number, ok := scalar.AsFloat()
	if !ok {
		return nil, joTypeErr(n, v)
	}

	return number, nil
}

// union converts against the first member type that accepts the value.
//
// Members are tried in document order and the per-member explanations are collected on the way,
// rather than probing silently and re-running to explain as salad's own union validator does.
// The reason is side effects: a member that is a File stats and hashes the file, so a second
// pass would read every candidate file twice, and the errors this pass discards on success cost
// one allocation on a path that has already done I/O.
func (l *joLoader) union(ctx context.Context, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	options := v.typ.Options()
	problems := make([]*salad.Error, 0, len(options))

	for _, option := range options {
		value, err := l.value(ctx, n, v.withType(option))
		if err == nil {
			return value, nil
		}

		problems = append(problems, err)
	}

	summary := fmt.Sprintf("%s: no type in %s accepts this %s", v.path, v.typ, salad.NodeKind(n))

	return nil, salad.Group(joNodeLoc(n), summary, problems...)
}

// array converts a sequence, element by element.
func (l *joLoader) array(ctx context.Context, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	schema := v.typ.Array()
	if schema == nil {
		return nil, salad.Errorf(joNodeLoc(n), "%s: array type carries no item schema", v.path)
	}

	seq, ok := salad.AsSeq(n)
	if !ok {
		return nil, joTypeErr(n, v)
	}

	values := make([]any, 0, seq.Len())

	for i, node := range seq.All() {
		value, err := l.value(ctx, node, v.item(i, schema.Items))
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

// record converts a mapping against an inline record schema.
func (l *joLoader) record(ctx context.Context, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	schema := v.typ.Record()
	if schema == nil {
		return nil, salad.Errorf(joNodeLoc(n), "%s: record type carries no schema", v.path)
	}

	m, ok := salad.AsMap(n)
	if !ok {
		return nil, joTypeErr(n, v)
	}

	names := make([]string, 0, len(schema.Fields))
	for i := range schema.Fields {
		names = append(names, ShortName(schema.Fields[i].Name))
	}

	unknown := joCheckKeys(m, names, "field of "+v.path)
	if unknown != nil {
		return nil, unknown
	}

	values := make(map[string]any, len(schema.Fields))

	for i := range schema.Fields {
		value, err := l.field(ctx, m, &schema.Fields[i], names[i], v)
		if err != nil {
			return nil, err
		}

		values[names[i]] = value
	}

	return values, nil
}

// field converts one record field. A record field carries no `default`, so an absent field is
// null when the field type permits it and an error otherwise.
//
// A field is a declaration in its own right: the schema gives CommandInputRecordField the same
// loadContents, loadListing and format that an input parameter has, so a File or Directory bound
// to one is subject to exactly the checks and the population a top-level input would be. Getting
// this wrong is silent — the conformance suite's record-in-format.cwl declares a format on a File
// field precisely to catch a runner that only looks at top-level parameters.
func (l *joLoader) field(
	ctx context.Context, m *salad.MapNode, f *cwlcore.RecordField, name string, v *joValueCtx,
) (any, *salad.Error) {
	nested := v.at("."+name, f.Type)
	nested.loadContents = f.LoadContents
	nested.listing = cmp.Or(f.LoadListing, l.listing)
	nested.format = joAllowedFormats(f.Format)

	node, ok := m.Get(name)
	if ok && !salad.IsNull(node) {
		return l.value(ctx, node, nested)
	}

	if f.Type.IsOptional() {
		return nil, nil
	}

	return nil, salad.Errorf(m.Loc(),
		"%s: field %q is required, and its type %s does not accept null", v.path, name, f.Type)
}

// joEnumValue converts a string against an inline enum schema. Symbols are resolved identifiers,
// so they are compared by short name, which is how a document writes them.
func joEnumValue(n salad.Node, v *joValueCtx) (any, *salad.Error) {
	schema := v.typ.Enum()
	if schema == nil {
		return nil, salad.Errorf(joNodeLoc(n), "%s: enum type carries no schema", v.path)
	}

	symbol, ok := salad.AsString(n)
	if !ok {
		return nil, joTypeErr(n, v)
	}

	names := make([]string, 0, len(schema.Symbols))
	for _, declared := range schema.Symbols {
		names = append(names, ShortName(declared))
	}

	if !slices.Contains(names, symbol) {
		return nil, salad.Errorf(joNodeLoc(n),
			"%s: %q is not one of the enum symbols %s", v.path, symbol, joJoinQuoted(names))
	}

	return symbol, nil
}

// freeform walks a value with no declared structure — the payload of an Any, of a named type, or
// of an undeclared corner of a mapping — converting it to plain Go values and normalising any
// File or Directory object it finds along the way.
func (l *joLoader) freeform(ctx context.Context, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	switch node := n.(type) {
	case *salad.MapNode:
		return l.freeformMap(ctx, node, v)
	case *salad.SeqNode:
		return l.freeformSeq(ctx, node, v)
	default:
		return salad.ToAny(n), nil
	}
}

// freeformMap converts a mapping, recognising a File or a Directory by its class.
func (l *joLoader) freeformMap(ctx context.Context, m *salad.MapNode, v *joValueCtx) (any, *salad.Error) {
	switch joClassOf(m) {
	case cwlcore.ClassFile:
		return l.normalizeFile(ctx, m, v)
	case cwlcore.ClassDirectory:
		return l.normalizeDirectory(ctx, m, v)
	}

	values := make(map[string]any, m.Len())

	for _, entry := range m.Entries() {
		value, err := l.freeform(ctx, entry.Value, v.at("."+entry.Key, cwlcore.TypeRef{}))
		if err != nil {
			return nil, err
		}

		values[entry.Key] = value
	}

	return values, nil
}

// freeformSeq converts a sequence with no declared item type.
func (l *joLoader) freeformSeq(ctx context.Context, seq *salad.SeqNode, v *joValueCtx) (any, *salad.Error) {
	values := make([]any, 0, seq.Len())

	for i, node := range seq.All() {
		value, err := l.freeform(ctx, node, v.item(i, cwlcore.TypeRef{}))
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

// joClassOf returns the `class` of a mapping when it is a string, and "" otherwise.
func joClassOf(m *salad.MapNode) string {
	node, ok := m.Get(joKeyClass)
	if !ok {
		return ""
	}

	class, _ := salad.AsString(node)

	return class
}

// joTypeErr reports a value of the wrong shape for its declared type.
func joTypeErr(n salad.Node, v *joValueCtx) *salad.Error {
	return salad.Errorf(joNodeLoc(n), "%s: expected %s, but found %s", v.path, joDescribeType(v.typ), salad.NodeKind(n))
}

// joDescribeType renders a type for a diagnostic, spelling out a File or a Directory as the class
// its value must declare.
func joDescribeType(typ cwlcore.TypeRef) string {
	if typ.Kind() == cwlcore.TypeKindStdin {
		return "a mapping with class: " + cwlcore.PrimitiveFile
	}

	name := typ.String()
	if typ.Kind() == cwlcore.TypeKindPrimitive &&
		(name == cwlcore.PrimitiveFile || name == cwlcore.PrimitiveDirectory) {
		return "a mapping with class: " + name
	}

	return strings.TrimSpace(name)
}
