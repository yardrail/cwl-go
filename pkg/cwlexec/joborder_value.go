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

// Type-checking and conversion of individual job-order values against declared CWL types.

// joValueCtx carries the type, base directory, path breadcrumb, and settings for one conversion position.
type joValueCtx struct {
	typ          cwlcore.TypeRef
	base         string
	path         string
	format       []string
	listing      cwlcore.LoadListingEnum
	loadContents bool
}

// withType returns a copy of v expecting typ at the same position.
func (v *joValueCtx) withType(typ cwlcore.TypeRef) *joValueCtx {
	next := *v
	next.typ = typ

	return &next
}

// at returns a copy of v for a nested position, resetting per-declaration settings.
func (v *joValueCtx) at(step string, typ cwlcore.TypeRef) *joValueCtx {
	next := *v
	next.typ = typ
	next.path += step
	next.loadContents = false
	next.listing = ""
	next.format = nil

	return &next
}

// item returns a copy of v for array element i, preserving loadContents/loadListing/format.
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
		return l.fileValue(ctx, n, v)
	default:
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

// joFloatValue converts a float or double. Integers widen. Preserves the original decimal literal.
func joFloatValue(scalar *salad.ScalarNode, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	number, ok := scalar.AsFloat()
	if !ok {
		return nil, joTypeErr(n, v)
	}

	if literal, written := scalar.AsDecimal(); written {
		return literal, nil
	}

	return number, nil
}

// union tries each member type in order, returning the first match.
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

// field converts one record field. Absent fields are null if allowed, error otherwise.
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

// joEnumValue converts a string against an inline enum schema, comparing by short name.
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

// freeform converts untyped values, normalising any File/Directory objects found.
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
	node, ok := m.Get(outKeyClass)
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

// joDescribeType renders a type for diagnostics.
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
