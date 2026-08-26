package cwlexec

import (
	"errors"
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Collecting a record-typed output, field by field.
//
// CommandLineTool.yml gives CommandOutputRecordField an `outputBinding` of its own, so a record
// output is not one thing globbed and then split up: it is several independent collections, one per
// field, assembled into an object keyed by the field's short name. Nothing about a field's binding
// differs from a parameter's — glob, loadContents, loadListing, outputEval, secondaryFiles and
// format all mean what they always mean — so this file adds no collection machinery at all. It adds
// the projection ([outTarget]) that lets a parameter and a record field be handed to the same
// collector, the type walk that finds the record, and the recursion over the fields.
//
// Records nest: a field may itself be a record whose own fields carry the bindings. The recursion
// terminates because [cwlcore.ResolveTypeRef] refuses to expand a type that reaches itself and
// leaves the cycle-closing edge as the TypeKindNamed reference it was written as. A named reference
// is not a record, so the walk stops there and the field is collected — or, having no binding,
// reported — like any other.
//
// Except when the record's own declaration carries an outputEval. CommandLineTool.yml gives
// outputEval no type restriction — "Evaluate an expression to generate the output value" — so a
// record-typed parameter may name one and produce the whole record itself, `self` bound to the array
// of globbed matches by exactly the usual rule. That is what `record_outputeval` and
// `record_outputeval_nojs` do, and there is nothing for the fields' bindings to add: the expression
// has already produced every field, and running the fields' own collection over the top would
// discard it.
//
// So the rule this file implements, applied identically to a parameter and to a nested record field:
//
//   - a declaration whose binding carries an outputEval is collected through that binding, whatever
//     its type — the expression's result is the value;
//   - otherwise, a declaration whose type reaches a record is collected field by field, from the
//     bindings the fields carry themselves.
//
// The two cannot both apply, and outputEval is the discriminator rather than "the binding exists"
// because glob, loadContents and loadListing describe how files are found, which for a record is
// still the fields' business; only outputEval describes what the value *is*. A record declaration
// carrying a binding with a glob and no outputEval therefore still collects field by field, and its
// glob is inert — the same thing the reference implementation does with it.

// ErrOutputUnbound reports a record output field that declares no outputBinding and whose type does
// not admit null.
//
// Nothing else can ever fill such a field. A parameter without a binding is a different case: the
// output object it belongs to has other producers — a tool's cwl.output.json, an extension handler
// merging its own results — so "this one is null" is a legitimate answer there, and it is the answer
// [outputCollector.bound] gives. A record field is reachable only through the walk in this file, so
// a field nothing collects is a document that declared a value it never described, and publishing
// null for a required File would put an object that does not satisfy its own declared type into the
// workflow. Optional fields are null, on the same terms as a glob that matched nothing.
var ErrOutputUnbound = errors.New("record output field declares no outputBinding")

// outTarget is the part of an output declaration that governs how one value is collected: the
// binding to collect it through, the type that says how to reduce what was collected, and the two
// decorations applied to the result.
//
// A [cwlcore.CommandOutputParameter] and a [cwlcore.RecordField] carry the same four things under
// the same names, and the collector needs nothing else from either. Projecting both onto one value
// is what lets a record field be collected by the parameter machinery rather than by a second copy
// of it.
type outTarget struct {
	// binding is the output binding the declaration carries, or nil when it carries none.
	binding *cwlcore.CommandOutputBinding

	// secondaryFiles are the declaration's secondaryFiles patterns.
	secondaryFiles []cwlcore.SecondaryFileSchema

	// format is the declaration's format, which holds at most one entry on an output.
	format []cwlcore.Expression

	// typ is the declaration's type, with named references already resolved.
	typ cwlcore.TypeRef
}

// parameterTarget projects an output parameter onto the part of it collection reads.
//
// The type is resolved against the tool's own SchemaDefRequirement on the way through, because a
// record declared there arrives as a bare name and a name has no fields to collect. Resolution
// descends through arrays, unions and record fields, so one call here resolves the whole tree.
//
// [CollectOutputs] is handed a tool and not a requirement scope, so a SchemaDefRequirement the tool
// inherits from an enclosing workflow is not visible here and such a type stays unresolved. That is
// the same gap loadListing had, and it has the same fix: the caller resolves it into the per-
// invocation copy of the tool it already builds.
func (c *outputCollector) parameterTarget(param *cwlcore.CommandOutputParameter) *outTarget {
	return &outTarget{
		binding:        param.OutputBinding,
		secondaryFiles: param.SecondaryFiles,
		format:         param.Format,
		typ:            cwlcore.ResolveTypeRef(c.scope, param.Type),
	}
}

// outFieldTarget projects one record field onto the part of it collection reads.
//
// The type needs no resolving: it was resolved with the enclosing parameter's, since
// [cwlcore.ResolveTypeRef] substitutes into every field of every record it walks through.
func outFieldTarget(field *cwlcore.RecordField) *outTarget {
	return &outTarget{
		binding:        field.OutputBinding,
		secondaryFiles: field.SecondaryFiles,
		format:         field.Format,
		typ:            field.Type,
	}
}

// outRecordShape is the record a declared type collects into, and how many levels of array wrap it.
// A nil schema means the type reaches no record at all.
//
// The two travel together as one value rather than as two results because gocritic's unnamedResult
// and revive's nonamedreturns between them leave a two-result function with nowhere to put the
// names, and "schema, depth" needs its names more than most.
type outRecordShape struct {
	// schema is the record schema the type reaches, or nil when it reaches none.
	schema *cwlcore.RecordSchema

	// depth is the number of array levels between the declared type and that schema.
	depth int
}

// record collects a record value from its fields' own bindings, wrapping it in as many levels of
// array as the type it was found under has.
//
// A record reached through `type: {type: array, items: <record>}` collects one record — one set of
// field bindings describes one record's worth of collection — and that record is the array's single
// element rather than the value itself, so that what is published satisfies the type the document
// declared.
func (c *outputCollector) record(shape outRecordShape) (any, error) {
	object, err := c.recordFields(shape.schema)
	if err != nil {
		return nil, err
	}

	value := any(object)
	for range shape.depth {
		value = []any{value}
	}

	return value, nil
}

// recordFields collects every field of a record schema into an object keyed by short name.
//
// Fields are visited in declaration order. Nothing observable depends on the order — the result is a
// map — but a document whose second field fails should report that field and not whichever one Go's
// map iteration reached first.
func (c *outputCollector) recordFields(schema *cwlcore.RecordSchema) (map[string]any, error) {
	object := make(map[string]any, len(schema.Fields))

	for index := range schema.Fields {
		field := &schema.Fields[index]
		name := ShortName(field.Name)

		value, err := c.recordField(field)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}

		object[name] = value
	}

	return object, nil
}

// recordField collects one field of a record.
//
// The record check comes second, so a field that is itself a record needs no binding of its own —
// its fields carry them — unless it names an outputEval, which produces the nested record whole.
// Only a field that collects a value directly needs a binding, and one that has none is null or an
// error according to its type; see [ErrOutputUnbound].
func (c *outputCollector) recordField(field *cwlcore.RecordField) (any, error) {
	target := outFieldTarget(field)

	shape := outRecordType(target.typ)
	if shape.schema != nil && !outEvaluatesWhole(target.binding) {
		return c.record(shape)
	}

	if target.binding == nil {
		return nil, outUnbound(target.typ)
	}

	return c.bound(target)
}

// outEvaluatesWhole reports whether a binding produces its declaration's value outright, which is
// what an outputEval does and what nothing else in an output binding does.
//
// It is the one thing that lets a record-typed declaration be collected through its own binding
// instead of through its fields'; see the rule at the head of this file.
func outEvaluatesWhole(binding *cwlcore.CommandOutputBinding) bool {
	return binding != nil && binding.OutputEval != ""
}

// outUnbound reports a field nothing collects, unless its type admits null.
func outUnbound(declared cwlcore.TypeRef) error {
	if declared.IsOptional() {
		return nil
	}

	return fmt.Errorf("%w: type %s admits no null", ErrOutputUnbound, declared)
}

// outRecordType finds the record schema a declared type collects into, and how many levels of array
// wrap it. It returns a nil schema when the type reaches no record at all.
//
// The walk descends through arrays and unions because that is what [cwlcore.ResolveTypeRef] descends
// through: a record declared by a SchemaDefRequirement is just as reachable as `["null", <record>]`
// or as an array's item type, and a document that writes one of those still expects its fields'
// bindings to be honoured. It does not descend through a TypeKindNamed reference, which is both what
// an unresolvable name looks like and what a recursive type's cycle-closing edge looks like.
func outRecordType(declared cwlcore.TypeRef) outRecordShape {
	switch declared.Kind() {
	case cwlcore.TypeKindRecord:
		return outRecordShape{schema: declared.Record()}
	case cwlcore.TypeKindArray:
		return outArrayRecordType(declared)
	case cwlcore.TypeKindUnion:
		return outUnionRecordType(declared)
	default:
		// A primitive, an enum, a standard-stream shortcut, an unresolved name or the unset
		// zero value: none of them has fields to collect.
		return outRecordShape{}
	}
}

// outArrayRecordType finds the record an array's items reach, one level deeper than they are.
func outArrayRecordType(declared cwlcore.TypeRef) outRecordShape {
	schema := declared.Array()
	if schema == nil {
		return outRecordShape{}
	}

	shape := outRecordType(schema.Items)
	if shape.schema == nil {
		return outRecordShape{}
	}

	shape.depth++

	return shape
}

// outUnionRecordType finds the record a union reaches through its first member that reaches one,
// which for the `["null", <record>]` an optional record expands to is the record itself.
func outUnionRecordType(declared cwlcore.TypeRef) outRecordShape {
	for _, option := range declared.Options() {
		shape := outRecordType(option)
		if shape.schema != nil {
			return shape
		}
	}

	return outRecordShape{}
}
