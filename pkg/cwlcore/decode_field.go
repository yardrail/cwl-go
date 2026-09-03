package cwlcore

import (
	"slices"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// The field-reading layer every decode_*.go file is built from.
//
// Decoding runs over a tree pkg/salad has already validated against the CWL
// schema, so the shapes here are expected rather than hoped for. The accessors
// are written accordingly: an absent field yields the Go zero value silently,
// and only a value of the wrong shape records an error. That keeps the per-record
// decoders to one straight-line struct literal each, which is what holds them
// under revive's complexity limits.
//
// Every accessor tolerates a nil *salad.MapNode, so a decoder that has already
// reported "this is not a mapping" can carry on and collect the rest of the
// document's problems rather than stopping at the first one.

// Document keys read while decoding. They are constants because the same key is
// read from several records — "id", "type" and "class" from almost all of them —
// and a typo in one copy would silently decode that record's field as absent.
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

// fieldNode returns the value bound to key, or nil when the key is absent or
// explicitly null. The two are deliberately not distinguished: every CWL field
// that this package models treats an explicit null exactly as it treats an
// absent field.
func fieldNode(m *salad.MapNode, key string) salad.Node {
	value, ok := m.Get(key)
	if !ok || salad.IsNull(value) {
		return nil
	}

	return value
}

// decoder collects the errors raised while turning one validated salad tree
// into typed values.
//
// Errors accumulate rather than short-circuit, so a single decode reports every
// malformed field it finds instead of only the first. Each decode entry point
// builds its own decoder; a decoder is not safe for concurrent use.
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

// err returns the accumulated errors as one value, or nil when the run was
// clean. A single error is returned unwrapped so that the common case reads as
// one line; several are grouped under a *salad.Error tree.
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

// errOr returns the accumulated errors, or a fresh one at loc when the run
// recorded none.
//
// It is how an entry point that must return either a value or an error keeps
// that promise: a decoder that produced nothing has always said why, and this
// says it on the decoder's behalf if it somehow did not, so that a nil error can
// never accompany a nil result.
func (d *decoder) errOr(loc salad.SourceLine, msg string) error {
	err := d.err()
	if err != nil {
		return err
	}

	return salad.Errorf(loc, "%s", msg)
}

// mapping returns n as a mapping, recording an error naming what was expected
// when it is anything else. The result may be nil, which every accessor in this
// file tolerates.
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
//
// It stays silent when the enclosing mapping is nil, because that only happens
// after the mapping itself has already been reported as malformed, and a second
// error about a field of a thing that is not a mapping would have nowhere to
// point.
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

// lenientText reads a string field without recording an error when the value is
// something else. It is for fields the schema types as Any, where a value this
// package cannot use is expected rather than exceptional.
func lenientText(m *salad.MapNode, key string) string {
	text, _ := salad.AsString(fieldNode(m, key))

	return text
}

// expression reads a field whose schema type is `string | Expression`. The two
// members are indistinguishable at this layer — the schema models an expression
// as a placeholder enum over strings — so both land on Expression.
func (d *decoder) expression(m *salad.MapNode, key string) Expression {
	return Expression(d.text(m, key))
}

// flag reads an optional boolean whose schema default is false, so that the Go
// zero value already carries the right meaning for an absent field.
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

// optBool reads a boolean field whose schema default is true, so that absent and
// present-and-false must stay distinguishable.
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

// optInt reads an integer field whose zero is a legal document value, so that
// absent and present-and-zero must stay distinguishable.
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

// optText reads a string field whose empty value is a legal document value, so
// that absent and present-and-empty must stay distinguishable.
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

// textList reads a field whose schema type is `T | T[]`, normalizing both forms
// into the slice the model declares. It returns nil when the field is absent, so
// that an absent list stays distinguishable from an empty one.
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

// oneOrMany returns the items of a field the schema types as `T | T[]`: a
// sequence yields its items, and any other value is a one-element list. It
// returns nil when the field is absent.
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

// listItems returns the items of a field the schema types as an array.
//
// The specification lets any array field carrying a jsonldPredicate mapSubject
// be written as a mapping keyed by that subject, and pkg/salad expands the form
// while resolving a document. Doing it again here is not redundant: Decode is a
// public entry point that a caller may reach with a tree it resolved itself, and
// an unexpanded mapping would otherwise decode as nothing at all. Pass an empty
// subject for the array fields the schema gives no mapSubject.
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

// identifierMap expands the identifier-map form of an array field into the
// sequence of objects the specification defines. Keys are visited in sorted
// order, matching pkg/salad, because the result is a set of named objects whose
// written order carries no meaning.
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

// identifierEntry builds one item of an expanded identifier map, moving the map
// key into the field named by the subject.
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

// decodeEach maps the items of a decoded list through fn, preserving the
// distinction between an absent list (nil) and an empty one.
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
