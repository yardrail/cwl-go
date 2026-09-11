package cwlcore

import (
	"slices"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Field-reading layer for all decode_*.go files.
// Absent fields yield zero values; wrong-shape values record errors.

// Document keys read while decoding.
const (
	keyClass          = "class"
	keyID             = "id"
	keyName           = "name"
	keyLabel          = "label"
	keyDoc            = "doc"
	keyType           = "type"
	keyItems          = "items"
	keyFields         = "fields"
	keySymbols        = "symbols"
	keyInputs         = "inputs"
	keyOutputs        = "outputs"
	keyRequirements   = "requirements"
	keyHints          = "hints"
	keyIntent         = "intent"
	keyCWLVersion     = "cwlVersion"
	keySecondaryFiles = "secondaryFiles"
	keyFormat         = "format"
	keyStreamable     = "streamable"
	keyLoadContents   = "loadContents"
	keyLoadListing    = "loadListing"
	keyDefault        = "default"
	keyInputBinding   = "inputBinding"
	keyOutputBinding  = "outputBinding"
	keyPattern        = "pattern"
	keyRequired       = "required"
	keyExpression     = "expression"
	keySteps          = "steps"
	keyGraph          = "$graph"
)

// nodeLoc reports where n came from, tolerating a nil node.
func nodeLoc(n salad.Node) salad.SourceLine {
	if n == nil {
		return salad.SourceLine{
			File:  "",
			Start: salad.Position{Line: 0, Column: 0, Offset: 0},
			End:   salad.Position{Line: 0, Column: 0, Offset: 0},
		}
	}

	return n.Loc()
}

// fieldNode returns the value bound to key, or nil when absent or null.
func fieldNode(m *salad.MapNode, key string) salad.Node {
	value, ok := m.Get(key)
	if !ok || salad.IsNull(value) {
		return nil
	}

	return value
}

// decoder accumulates errors while decoding a salad tree into typed values. Not concurrent-safe.
type decoder struct {
	errs   []*salad.Error
	loaded *salad.LoadedSchema
}

type decoderOption func(*decoder)

func withLoadedSchema(ls *salad.LoadedSchema) decoderOption {
	return func(d *decoder) { d.loaded = ls }
}

// newDecoder starts one decode run.
func newDecoder(opts ...decoderOption) *decoder {
	d := &decoder{errs: make([]*salad.Error, 0), loaded: nil}
	for _, o := range opts {
		o(d)
	}

	return d
}

// failf records a decoding error at loc.
func (d *decoder) failf(loc salad.SourceLine, format string, a ...any) {
	d.errs = append(d.errs, salad.Errorf(loc, format, a...))
}

// err returns the accumulated errors as one value, or nil.
func (d *decoder) err() error {
	switch len(d.errs) {
	case 0:
		return nil
	case 1:
		return d.errs[0]
	default:
		return salad.Group(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"the document could not be decoded",
			d.errs...)
	}
}

// errOr returns accumulated errors, or a fallback error at loc if none were recorded.
func (d *decoder) errOr(loc salad.SourceLine, msg string) error {
	err := d.err()
	if err != nil {
		return err
	}

	return salad.Errorf(loc, "%s", msg)
}

// mapping returns n as a mapping, recording an error if it is not one.
func (d *decoder) mapping(n salad.Node, what string) *salad.MapNode {
	if n == nil {
		d.failf(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"%s is missing",
			what,
		)

		return nil
	}

	m, ok := salad.AsMap(n)
	if !ok {
		d.failf(n.Loc(), "%s must be a mapping, but it is %s", what, salad.NodeKind(n))

		return nil
	}

	return m
}

// missingField records a required field that the document did not supply.
func (d *decoder) missingField(m *salad.MapNode, key, what string) {
	if m == nil {
		return
	}

	d.failf(m.Loc(), "%s must declare a %q field", what, key)
}

// text reads a string field, or "" when it is absent.
func (d *decoder) text(m *salad.MapNode, key string) string {
	value := fieldNode(m, key)
	if value == nil {
		return ""
	}

	text, ok := salad.AsString(value)
	if !ok {
		d.failf(value.Loc(), "the %q field must be a string, but it is %s", key, salad.NodeKind(value))

		return ""
	}

	return text
}

// lenientText reads a string field, returning "" without error if the value is not a string.
func lenientText(m *salad.MapNode, key string) string {
	text, _ := salad.AsString(fieldNode(m, key))

	return text
}

// expression reads a string-or-Expression field.
func (d *decoder) expression(m *salad.MapNode, key string) Expression {
	return Expression(d.text(m, key))
}

// flag reads an optional boolean field, defaulting to false.
func (d *decoder) flag(m *salad.MapNode, key string) bool {
	value := fieldNode(m, key)
	if value == nil {
		return false
	}

	scalar, ok := salad.AsScalar(value)
	if !ok || !scalar.IsBool() {
		d.failf(value.Loc(), "the %q field must be a boolean, but it is %s", key, salad.NodeKind(value))

		return false
	}

	return scalar.AsBool()
}

// optBool reads an optional boolean field where absent differs from false.
func (d *decoder) optBool(m *salad.MapNode, key string) OptBool {
	value := fieldNode(m, key)
	if value == nil {
		return OptBool{value: false, set: false}
	}

	scalar, ok := salad.AsScalar(value)
	if !ok || !scalar.IsBool() {
		d.failf(value.Loc(), "the %q field must be a boolean, but it is %s", key, salad.NodeKind(value))

		return OptBool{value: false, set: false}
	}

	return NewOptBool(scalar.AsBool())
}

// optInt reads an optional integer field where absent differs from zero.
func (d *decoder) optInt(m *salad.MapNode, key string) OptInt {
	value := fieldNode(m, key)
	if value == nil {
		return OptInt{value: 0, set: false}
	}

	number, ok := integerOf(value)
	if !ok {
		d.failf(value.Loc(), "the %q field must be an integer, but it is %s", key, salad.NodeKind(value))

		return OptInt{value: 0, set: false}
	}

	return NewOptInt(number)
}

// optText reads an optional string field where absent differs from empty.
func (d *decoder) optText(m *salad.MapNode, key string) OptString {
	value := fieldNode(m, key)
	if value == nil {
		return OptString{value: "", set: false}
	}

	text, ok := salad.AsString(value)
	if !ok {
		d.failf(value.Loc(), "the %q field must be a string, but it is %s", key, salad.NodeKind(value))

		return OptString{value: "", set: false}
	}

	return NewOptString(text)
}

// integerOf reads a node as an int64.
func integerOf(n salad.Node) (int64, bool) {
	scalar, ok := salad.AsScalar(n)
	if !ok {
		return 0, false
	}

	return scalar.AsInt()
}

// textList reads a string-or-string-array field, normalizing to a slice.
func (d *decoder) textList(m *salad.MapNode, key string) []string {
	items := d.oneOrMany(m, key)
	if items == nil {
		return nil
	}

	out := make([]string, 0, len(items))

	for _, item := range items {
		text, ok := salad.AsString(item)
		if !ok {
			d.failf(item.Loc(), "the %q field must hold strings, but it holds %s", key, salad.NodeKind(item))

			continue
		}

		out = append(out, text)
	}

	return out
}

// expressionList is textList for a field whose members may embed expressions.
func (d *decoder) expressionList(m *salad.MapNode, key string) []Expression {
	texts := d.textList(m, key)
	if texts == nil {
		return nil
	}

	out := make([]Expression, 0, len(texts))
	for _, text := range texts {
		out = append(out, Expression(text))
	}

	return out
}

// intList reads a field whose schema type is `int[]`.
func (d *decoder) intList(m *salad.MapNode, key string) []int {
	items := d.oneOrMany(m, key)
	if items == nil {
		return nil
	}

	out := make([]int, 0, len(items))

	for _, item := range items {
		number, ok := integerOf(item)
		if !ok {
			d.failf(item.Loc(), "the %q field must hold integers, but it holds %s", key, salad.NodeKind(item))

			continue
		}

		out = append(out, int(number))
	}

	return out
}

// oneOrMany normalizes a `T | T[]` field into a node slice. Nil if absent.
func (d *decoder) oneOrMany(m *salad.MapNode, key string) []salad.Node {
	value := fieldNode(m, key)
	if value == nil {
		return nil
	}

	if seq, ok := salad.AsSeq(value); ok {
		return seq.Items()
	}

	return []salad.Node{value}
}

// listItems returns items of an array field, expanding identifier-map form if needed.
func (d *decoder) listItems(m *salad.MapNode, key, subject, predicate string) []salad.Node {
	value := fieldNode(m, key)
	if value == nil {
		return nil
	}

	if seq, ok := salad.AsSeq(value); ok {
		return seq.Items()
	}

	nested, ok := salad.AsMap(value)
	if !ok || subject == "" {
		d.failf(value.Loc(), "the %q field must be a sequence, but it is %s", key, salad.NodeKind(value))

		return nil
	}

	return d.identifierMap(key, subject, predicate, nested)
}

// identifierMap expands a map-form array into a sequence of objects, sorted by key.
func (d *decoder) identifierMap(key, subject, predicate string, m *salad.MapNode) []salad.Node {
	keys := m.Keys()
	slices.Sort(keys)

	out := make([]salad.Node, 0, len(keys))

	for _, name := range keys {
		// The key came from Keys, so the lookup cannot miss.
		value, _ := m.Get(name)
		out = append(out, d.identifierEntry(key, subject, predicate, name, value))
	}

	return out
}

// identifierEntry builds one item of an expanded identifier map.
func (d *decoder) identifierEntry(key, subject, predicate, name string, value salad.Node) salad.Node {
	loc := nodeLoc(value)

	object, ok := salad.AsMap(value)
	if !ok {
		if predicate == "" {
			d.failf(loc, "the %q field: the value of %q is %s, and %q assigns no field to a bare value",
				key, name, salad.NodeKind(value), key)

			return value
		}

		object = salad.NewMapNode(loc, []salad.MapEntry{{Key: predicate, Value: value}})
	}

	if existing, has := object.Get(subject); has && !salad.IsNull(existing) {
		return object
	}

	return object.With(salad.MapEntry{Key: subject, Value: salad.NewStringNode(loc, name)})
}

// decodeEach maps items through fn, preserving nil vs empty distinction.
func decodeEach[T any](items []salad.Node, fn func(salad.Node) T) []T {
	if items == nil {
		return nil
	}

	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, fn(item))
	}

	return out
}
