package cwlexec

import (
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Collecting the bindings: steps 1 to 3 of the specification's command-line algorithm.
//
// The walk descends the input schema and the input object together, emitting one boundArg per
// leaf binding it finds and giving each a sort key built from the positions of the levels leading
// to it. Rendering those leaves into argv elements is clt_render.go's job, and happens only after
// the whole set has been sorted — which is what step 5, "in the sorted order, apply the rules
// defined in CommandLineBinding", requires.
//
// One rule governs the whole walk, and is worth stating up front because it is why the schema is
// consulted so little: per CommandLineBinding, "if there is a mismatch between the type described
// by the input schema and the effective value, such as resulting from an expression evaluation, an
// implementation must use the data type of the effective value". The schema is therefore used only
// to find the nested bindings a value's own shape cannot reveal — an array's item binding, a
// record's field bindings — and every rendering decision is taken from the value.

// emptyBinding is what an array's items are bound with when the array's own schema declares no
// inputBinding but the parameter does: each item still has to reach the command line, with no
// prefix and no position of its own. It is never mutated.
var emptyBinding = &cwlcore.CommandLineBinding{}

// boundArg is one leaf binding: a binding, the value it applies to, and the sort key that decides
// where its command-line elements land.
type boundArg struct {
	// binding is the CommandLineBinding whose rules render this leaf. Never nil.
	binding *cwlcore.CommandLineBinding

	// value is the effective value, already through valueFrom when the binding declared one.
	value any

	// origin names the parameter or arguments entry this leaf came from, for diagnostics.
	origin string

	// key is the sort key assigned by steps 1 to 3.
	key sortKey

	// computed records that value is the result of evaluating valueFrom rather than the input
	// object's own value. It changes one rendering rule: a list produced by valueFrom has no
	// per-item bindings to fall back on, so its elements are emitted directly.
	computed bool
}

// bindTarget is one level of the schema-and-value walk.
type bindTarget struct {
	// typ is the declared type at this level, used only to find nested bindings.
	typ cwlcore.TypeRef

	// binding is the inputBinding declared at this level, or nil if there is none.
	binding *cwlcore.CommandLineBinding

	// value is the input object's value at this level.
	value any

	// origin names the enclosing parameter, for diagnostics.
	origin string

	// key is the sort key of the enclosing levels.
	key sortKey

	// tie is this level's tie-break element: the field or parameter name for a named level, and
	// the index for an array element. Spec step 3: "if and only if two bindings have the same
	// sort key, the tie must be broken using the ordering of the field or parameter name
	// immediately containing the leaf binding", and "for bindings on arrays and maps, the
	// sorting key must include the array index or map key following the position".
	tie keyElem
}

// cmdBuilder accumulates the leaf bindings of one command line.
type cmdBuilder struct {
	// eval evaluates the position and valueFrom expressions a binding may carry.
	eval *cwlcore.Evaluator

	// inputs is the resolved input object, keyed by parameter short name.
	inputs map[string]any

	// bound are the leaves collected so far, in collection order.
	bound []boundArg

	// scope is the requirement scope in effect, consulted only to resolve the named types a
	// SchemaDefRequirement declares.
	scope *cwlcore.RequirementScope

	// runtime is the runtime.* context expressions see.
	runtime cwlcore.RuntimeContext
}

// collect walks the tool's inputs and arguments, filling in b.bound.
//
// Inputs are collected before arguments, and both in document order, which is the order a stable
// sort falls back on for two bindings whose keys are wholly equal.
//
// Each parameter's type is resolved against the SchemaDefRequirement in scope exactly once, here
// at its root, rather than level by level during the walk. That is not only the cheaper of the two
// — [cwlcore.ResolveTypeRef] substitutes the whole tree, descending through arrays, unions and
// record fields — it is the correct one: a recursive declaration comes back with the edge that
// closes its cycle left as a bare name, and re-resolving that edge further down would expand it
// again and never terminate. Resolving once means the walk meets that residual name, finds nothing
// nested under it, and stops.
func (b *cmdBuilder) collect(tool *cwlcore.CommandLineTool) error {
	for index := range tool.Inputs {
		param := &tool.Inputs[index]
		name := ShortName(param.ID())

		target := &bindTarget{
			typ:     cwlcore.ResolveTypeRef(b.scope, param.Type),
			binding: param.InputBinding,
			value:   b.inputs[name],
			origin:  "input " + name,
			tie:     textKey(name),
		}

		err := b.bindInput(target)
		if err != nil {
			return fmt.Errorf("input %s: %w", name, err)
		}
	}

	for index := range tool.Arguments {
		err := b.bindArgument(index, tool.Arguments[index])
		if err != nil {
			return fmt.Errorf("arguments[%d]: %w", index, err)
		}
	}

	return nil
}

// bindArgument collects one CommandLineTool.arguments entry.
//
// Spec step 1 assigns an arguments entry the sort key [position, i], where i is its index in the
// list. Because i is numeric and a parameter's tie-break element is its name, and because numeric
// key elements sort before string ones, every argument sorts ahead of every input parameter that
// shares its position — which is the ordering the reference implementation produces too.
//
// All three spelling of an entry — a plain string, an expression, and a full CommandLineBinding —
// are normalized to a binding whose valueFrom carries the value, because that is what the
// specification says an arguments entry means: "for binding objects listed in
// CommandLineTool.arguments, the term 'value' refers to the effective value after evaluating
// valueFrom". A plain string is returned verbatim by the evaluator, so the normalization costs
// nothing.
func (b *cmdBuilder) bindArgument(index int, arg cwlcore.CommandLineArgument) error {
	binding := argumentBinding(arg)
	if binding == nil {
		return ErrArgumentValueFrom
	}

	position, err := b.position(binding, nil)
	if err != nil {
		return err
	}

	origin := fmt.Sprintf("arguments[%d]", index)
	key := sortKey(nil).child(numKey(position), numKey(int64(index)))

	return b.bindValueFrom(key, origin, binding, nil)
}

// argumentBinding normalizes one arguments entry into a binding carrying its value in valueFrom,
// or nil when the entry is a CommandLineBinding that declares no valueFrom.
func argumentBinding(arg cwlcore.CommandLineArgument) *cwlcore.CommandLineBinding {
	switch arg.Kind() {
	case cwlcore.ValueString:
		return &cwlcore.CommandLineBinding{ValueFrom: cwlcore.Expression(arg.Literal())}
	case cwlcore.ValueExpression:
		return &cwlcore.CommandLineBinding{ValueFrom: arg.Expression()}
	case cwlcore.ValueBinding:
		binding := arg.Binding()
		if binding == nil || binding.ValueFrom == "" {
			return nil
		}

		return binding
	default:
		return nil
	}
}

// bindInput collects the leaves for one level of the walk: an input parameter, a record field, or
// an array element.
//
// A null value adds nothing at all, "not even the prefix" — the binding rules end with "null: Add
// nothing", and nothing distinguishes a prefix from the rest of what a binding would have added.
// That check comes first, so it also short-circuits valueFrom: "if the value of the associated
// input parameter is null, valueFrom is not evaluated and nothing is added to the command line".
func (b *cmdBuilder) bindInput(target *bindTarget) error {
	if target.value == nil {
		return nil
	}

	key := target.key
	binding := target.binding

	if binding != nil {
		position, err := b.position(binding, target.value)
		if err != nil {
			return err
		}

		key = key.child(numKey(position), target.tie)

		if binding.ValueFrom != "" {
			return b.bindValueFrom(key, target.origin, binding, target.value)
		}

		b.add(key, target.origin, binding, target.value)
	}

	return b.bindNested(target, key)
}

// bindNested descends into the structure a value's type describes, looking for the bindings that
// live below this level. Only arrays, records and enums can carry one; every other type is a leaf.
//
// key is this level's key, already extended by bindInput when this level had a binding of its own,
// so a nested binding's key always begins with the keys of the levels containing it.
func (b *cmdBuilder) bindNested(target *bindTarget, key sortKey) error {
	resolved := bindingType(target.typ, target.value)

	switch resolved.Kind() {
	case cwlcore.TypeKindArray:
		return b.bindArray(key, target, resolved.Array())
	case cwlcore.TypeKindRecord:
		return b.bindRecord(key, target, resolved.Record())
	case cwlcore.TypeKindEnum:
		return b.bindEnum(key, target, resolved.Enum())
	default:
		return nil
	}
}

// bindArray collects the bindings of an array's elements.
//
// The distinction the specification draws between the two bindings an array can have is the whole
// subtlety here:
//
//   - The binding on the *parameter* binds the array as a whole. Its leaf renders as "first add
//     prefix" — or, with itemSeparator, as the single joined argument.
//   - The binding on the array *schema*, `type: {type: array, items: ..., inputBinding: ...}`,
//     binds each element in turn. Its prefix is repeated once per element.
//
// Both may be present, and either may be absent. When the array schema declares none but the
// parameter does, the elements are still emitted — "otherwise, first add prefix, then recursively
// process individual elements" — with an empty binding standing in, unless itemSeparator has
// already claimed them for the joined form.
func (b *cmdBuilder) bindArray(key sortKey, target *bindTarget, schema *cwlcore.ArraySchema) error {
	items, ok := valueList(target.value)
	if !ok || schema == nil {
		return nil
	}

	itemBinding := itemBindingFor(schema, target.binding)
	if itemBinding == nil {
		return nil
	}

	for index, item := range items {
		element := &bindTarget{
			typ:     schema.Items,
			binding: itemBinding,
			value:   item,
			origin:  fmt.Sprintf("%s[%d]", target.origin, index),
			key:     key,
			tie:     numKey(int64(index)),
		}

		err := b.bindInput(element)
		if err != nil {
			return err
		}
	}

	return nil
}

// itemBindingFor picks the binding an array's elements are bound with, or nil when they are not
// bound individually at all.
func itemBindingFor(schema *cwlcore.ArraySchema, parent *cwlcore.CommandLineBinding) *cwlcore.CommandLineBinding {
	if schema.InputBinding != nil {
		return schema.InputBinding
	}

	if parent == nil || parent.ItemSeparator != "" {
		return nil
	}

	return emptyBinding
}

// bindRecord collects the bindings of a record's fields.
//
// The record's own value renders as "add prefix only"; its fields are then walked in schema order,
// each with its own binding, and each keyed by its field name so that two fields sharing a
// position order by name.
//
// A record schema may itself declare an inputBinding, separately from the one on the parameter
// whose type it is. That is a second level, so it contributes its own position to the key of every
// field below it and emits its own prefix.
func (b *cmdBuilder) bindRecord(key sortKey, target *bindTarget, schema *cwlcore.RecordSchema) error {
	object, ok := valueObject(target.value)
	if !ok || schema == nil {
		return nil
	}

	fieldKey, err := b.schemaLevel(key, target, schema.InputBinding)
	if err != nil {
		return err
	}

	for index := range schema.Fields {
		field := &schema.Fields[index]
		name := ShortName(field.Name)

		value := &bindTarget{
			typ:     field.Type,
			binding: field.InputBinding,
			value:   object[name],
			origin:  target.origin + "." + name,
			key:     fieldKey,
			tie:     textKey(name),
		}

		err := b.bindInput(value)
		if err != nil {
			return err
		}
	}

	return nil
}

// bindEnum collects the binding an enum schema may declare. An enum's value is a symbol, so there
// is nothing below it to walk; the schema's own binding is simply a second binding of the same
// value, alongside any the parameter declared.
func (b *cmdBuilder) bindEnum(key sortKey, target *bindTarget, schema *cwlcore.EnumSchema) error {
	if schema == nil {
		return nil
	}

	_, err := b.schemaLevel(key, target, schema.InputBinding)

	return err
}

// schemaLevel emits the leaf for a binding declared on an inline schema rather than on the
// parameter or field that uses it, and returns the key the levels below it hang from. A nil
// binding is not a level at all, and leaves the key unchanged.
func (b *cmdBuilder) schemaLevel(key sortKey, target *bindTarget,
	binding *cwlcore.CommandLineBinding,
) (sortKey, error) {
	if binding == nil {
		return key, nil
	}

	position, err := b.position(binding, target.value)
	if err != nil {
		return nil, err
	}

	nested := key.child(numKey(position), target.tie)
	b.add(nested, target.origin, binding, target.value)

	return nested, nil
}

// bindValueFrom evaluates a binding's valueFrom and emits the result as this binding's leaf.
//
// valueFrom replaces the value rather than decorating it, so the leaf is terminal: the declared
// type's structure is not walked, because the effective value need not have that structure at all.
// `self` is the value the binding is attached to, which for an arguments entry is null.
func (b *cmdBuilder) bindValueFrom(key sortKey, origin string,
	binding *cwlcore.CommandLineBinding, self any,
) error {
	value, err := b.eval.Eval(string(binding.ValueFrom), b.context(self))
	if err != nil {
		return fmt.Errorf("valueFrom %q: %w", string(binding.ValueFrom), err)
	}

	b.bound = append(b.bound, boundArg{
		binding:  binding,
		value:    value,
		origin:   origin,
		key:      key,
		computed: true,
	})

	return nil
}

// add records one leaf binding taken straight from the input object.
func (b *cmdBuilder) add(key sortKey, origin string, binding *cwlcore.CommandLineBinding, value any) {
	b.bound = append(b.bound, boundArg{binding: binding, value: value, origin: origin, key: key})
}

// position resolves a binding's `position` to the number its sort key uses.
//
// The schema's default is 0, so an absent position is 0. An expression is evaluated with `self`
// bound to the value being bound, as the schema requires: "if the inputBinding is associated with
// an input parameter, then the value of self will be the value of the input parameter". A null
// result is read as the default rather than rejected, since the schema admits it: "expressions
// must return a single value of type int or a null".
func (b *cmdBuilder) position(binding *cwlcore.CommandLineBinding, self any) (int64, error) {
	if binding.Position.Kind() != cwlcore.ValueExpression {
		return binding.Position.Int(), nil
	}

	expr := string(binding.Position.Expression())

	value, err := b.eval.Eval(expr, b.context(self))
	if err != nil {
		return 0, fmt.Errorf("position %q: %w", expr, err)
	}

	if value == nil {
		return 0, nil
	}

	position, ok := integerValue(value)
	if !ok {
		return 0, fmt.Errorf("%w: %q evaluated to %s", ErrBindingPosition, expr, cwlcore.TypeName(value))
	}

	return position, nil
}

// context is the symbol environment one of this command line's expressions is evaluated against.
func (b *cmdBuilder) context(self any) *cwlcore.EvalContext {
	return &cwlcore.EvalContext{Inputs: b.inputs, Self: self, Runtime: b.runtime}
}

// bindingType picks the union member that describes value's shape, so that the walk descends into
// the right nested schema. A non-union type is returned unchanged.
//
// Only arrays, records and enums can carry nested bindings, so nothing is lost when no member
// matches: the zero TypeRef ends the descent, which is the correct outcome for a scalar.
//
// A TypeKindNamed reference reaching this point ends the descent too, but by then it can only be
// one of the two references that have no schema to descend into: a name no SchemaDefRequirement in
// scope declares, or the edge closing a recursive declaration's cycle. Every other name was already
// substituted at the parameter's root — see [cmdBuilder.collect].
//
// Two passes, and the first is what makes a union of *records* usable at all. Shape alone cannot
// tell one record member from another — every one of them is an object — so a union like
// `[Map1, Map2, Map3, Map4]` would always resolve to its first member, and every field the other
// members declare and that one does not would silently never reach the command line. The first pass
// therefore asks which member actually accepts the value, and the second pass falls back to shape
// only when no member accepted it — so that a value no declared member fits still binds the way it
// used to rather than vanishing.
func bindingType(typ cwlcore.TypeRef, value any) cwlcore.TypeRef {
	if typ.Kind() != cwlcore.TypeKindUnion {
		return typ
	}

	options := typ.Options()

	for _, option := range options {
		if acceptsValue(option, value) {
			return option
		}
	}

	for _, option := range options {
		if describesValue(option, value) {
			return option
		}
	}

	return cwlcore.TypeRef{}
}

// describesValue reports whether option is the union member value takes its shape from.
func describesValue(option cwlcore.TypeRef, value any) bool {
	switch option.Kind() {
	case cwlcore.TypeKindArray:
		_, ok := valueList(value)

		return ok
	case cwlcore.TypeKindRecord:
		return isRecordValue(value)
	case cwlcore.TypeKindEnum:
		_, ok := value.(string)

		return ok
	default:
		return false
	}
}

// acceptsValue reports whether option not only has value's shape but admits its content. It is
// [describesValue] sharpened for the two kinds a union can hold several of and tell apart by
// content: a record, by which fields it declares, and an enum, by which symbols it permits.
func acceptsValue(option cwlcore.TypeRef, value any) bool {
	switch option.Kind() {
	case cwlcore.TypeKindRecord:
		return recordAccepts(option.Record(), value)
	case cwlcore.TypeKindEnum:
		return enumAccepts(option.Enum(), value)
	default:
		return describesValue(option, value)
	}
}

// recordAccepts reports whether value is a record at all, and if so whether every key of it names a
// field this schema declares and every field the schema declares admits what it holds for it.
//
// A schema-less record type accepts nothing: it declares no fields, so there is no evidence it is
// the member value came from, and the fallback pass in [bindingType] will reach it anyway if no
// other member fits.
func recordAccepts(schema *cwlcore.RecordSchema, value any) bool {
	object, ok := valueObject(value)
	if !ok || schema == nil || !isRecordValue(value) {
		return false
	}

	declared := make(map[string]*cwlcore.RecordField, len(schema.Fields))

	for index := range schema.Fields {
		field := &schema.Fields[index]
		declared[ShortName(field.Name)] = field
	}

	for name := range object {
		if _, found := declared[name]; !found {
			return false
		}
	}

	for name, field := range declared {
		if !fieldAccepts(field, object[name]) {
			return false
		}
	}

	return true
}

// fieldAccepts reports whether a record field's declared type admits the value present for it.
//
// Only the two checks that actually discriminate one record member of a union from another are
// made: a field with no value has to be optional, and a field declared as an enum has to permit the
// symbol given. Anything deeper would be re-validating the input object, which the job order has
// already been through.
func fieldAccepts(field *cwlcore.RecordField, value any) bool {
	if value == nil {
		return field.Type.IsOptional()
	}

	symbol, ok := value.(string)
	if !ok {
		return true
	}

	return enumOptionsAccept(field.Type, symbol)
}

// enumOptionsAccept reports whether symbol is one an enum-typed field admits. A type with no enum
// among its options constrains nothing here and accepts anything.
func enumOptionsAccept(typ cwlcore.TypeRef, symbol string) bool {
	options := []cwlcore.TypeRef{typ}
	if typ.Kind() == cwlcore.TypeKindUnion {
		options = typ.Options()
	}

	constrained := false

	for _, option := range options {
		if option.Kind() != cwlcore.TypeKindEnum {
			continue
		}

		if enumAccepts(option.Enum(), symbol) {
			return true
		}

		constrained = true
	}

	return !constrained
}

// enumAccepts reports whether an enum schema declares value as one of its symbols. Symbols are
// resolved to absolute identifiers, while the value carries the short spelling the document wrote,
// so both are compared.
func enumAccepts(schema *cwlcore.EnumSchema, value any) bool {
	symbol, ok := value.(string)
	if !ok || schema == nil {
		return false
	}

	for _, declared := range schema.Symbols {
		if declared == symbol || ShortName(declared) == symbol {
			return true
		}
	}

	return false
}
