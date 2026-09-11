package cwlexec

import (
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Steps 1–3 of the command-line algorithm: walk schema+value to collect leaf bindings with sort keys.
// Rendering into argv is in clt_render.go. Schema is consulted only for nested bindings; effective
// value type always wins per the spec.

// emptyBinding is the fallback binding for array items with no declared inputBinding.
var emptyBinding = &cwlcore.CommandLineBinding{
	Prefix:        "",
	ItemSeparator: "",
	ValueFrom:     "",
	Position:      cwlcore.ExprLong{},
	Separate:      cwlcore.OptBool{},
	ShellQuote:    cwlcore.OptBool{},
	LoadContents:  false,
}

// boundArg is one collected leaf binding with its sort key.
type boundArg struct {
	binding  *cwlcore.CommandLineBinding // never nil
	value    any                         // effective value (post-valueFrom if applicable)
	origin   string                      // parameter or arguments entry name, for diagnostics
	key      sortKey                     // sort key from steps 1–3
	computed bool                        // true when value came from valueFrom evaluation
}

// bindTarget is one level of the schema-and-value walk.
type bindTarget struct {
	typ     cwlcore.TypeRef             // declared type, used to find nested bindings
	binding *cwlcore.CommandLineBinding // inputBinding at this level, or nil
	value   any                         // input value at this level
	origin  string                      // enclosing parameter name, for diagnostics
	key     sortKey                     // sort key of enclosing levels
	tie     keyElem                     // tie-break: field/param name or array index
}

// cmdBuilder accumulates the leaf bindings of one command line.
type cmdBuilder struct {
	eval    *cwlcore.Evaluator        // evaluates position and valueFrom expressions
	inputs  map[string]any            // resolved inputs, keyed by short name
	bound   []boundArg                // collected leaves, in collection order
	scope   *cwlcore.RequirementScope // for resolving SchemaDefRequirement types
	runtime cwlcore.RuntimeContext    // runtime.* context for expressions
}

// collect walks the tool's inputs and arguments, filling in b.bound.
// Types are resolved once at the root via [cwlcore.ResolveTypeRef] to avoid infinite expansion of recursive types.
func (b *cmdBuilder) collect(tool *cwlcore.CommandLineTool) error {
	for index := range tool.Inputs {
		param := &tool.Inputs[index]
		name := ShortName(param.ID())

		target := &bindTarget{
			typ:     cwlcore.ResolveTypeRef(b.scope, param.Type),
			binding: param.InputBinding,
			value:   b.inputs[name],
			origin:  "input " + name,
			key:     nil,
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

// bindArgument collects one CommandLineTool.arguments entry, normalized to a valueFrom binding.
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

// argumentBinding normalizes an arguments entry to a binding with valueFrom, or nil if none.
func argumentBinding(arg cwlcore.CommandLineArgument) *cwlcore.CommandLineBinding {
	switch arg.Kind() {
	case cwlcore.ValueString:
		return &cwlcore.CommandLineBinding{
			Prefix:        "",
			ItemSeparator: "",
			ValueFrom:     cwlcore.Expression(arg.Literal()),
			Position:      cwlcore.ExprLong{},
			Separate:      cwlcore.OptBool{},
			ShellQuote:    cwlcore.OptBool{},
			LoadContents:  false,
		}
	case cwlcore.ValueExpression:
		return &cwlcore.CommandLineBinding{
			Prefix:        "",
			ItemSeparator: "",
			ValueFrom:     arg.Expression(),
			Position:      cwlcore.ExprLong{},
			Separate:      cwlcore.OptBool{},
			ShellQuote:    cwlcore.OptBool{},
			LoadContents:  false,
		}
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

// bindInput collects leaves for one level of the walk. Null values add nothing.
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

// bindNested descends into arrays, records, and enums to find nested bindings.
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

// bindArray collects bindings for each array element.
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

// itemBindingFor returns the per-element binding, or nil if elements are not individually bound.
func itemBindingFor(schema *cwlcore.ArraySchema, parent *cwlcore.CommandLineBinding) *cwlcore.CommandLineBinding {
	if schema.InputBinding != nil {
		return schema.InputBinding
	}

	if parent == nil || parent.ItemSeparator != "" {
		return nil
	}

	return emptyBinding
}

// bindRecord collects bindings for a record's fields in schema order.
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

// bindEnum collects a binding declared on an enum schema, if any.
func (b *cmdBuilder) bindEnum(key sortKey, target *bindTarget, schema *cwlcore.EnumSchema) error {
	if schema == nil {
		return nil
	}

	_, err := b.schemaLevel(key, target, schema.InputBinding)

	return err
}

// schemaLevel emits a leaf for a schema-level binding and returns the resulting key. Nil binding is a no-op.
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

// bindValueFrom evaluates valueFrom and emits the result as a terminal leaf.
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

// add records one leaf binding from the input object.
func (b *cmdBuilder) add(key sortKey, origin string, binding *cwlcore.CommandLineBinding, value any) {
	b.bound = append(b.bound, boundArg{binding: binding, value: value, origin: origin, key: key, computed: false})
}

// position resolves a binding's position to a sort key number. Default is 0; null expression results are 0.
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

// context builds the expression evaluation environment.
func (b *cmdBuilder) context(self any) *cwlcore.EvalContext {
	return &cwlcore.EvalContext{Inputs: b.inputs, Self: self, Runtime: b.runtime}
}

// bindingType picks the union member matching value. First pass checks acceptance (needed to
// disambiguate records); second pass falls back to shape matching.
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

// describesValue reports whether option matches value's shape.
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

// acceptsValue reports whether option matches value's shape and admits its content.
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

// recordAccepts reports whether value is a record whose keys and values match the schema's fields.
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

// fieldAccepts reports whether a field's type admits the given value.
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

// enumOptionsAccept reports whether symbol is permitted by any enum in the type's options.
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

// enumAccepts reports whether value is a symbol declared by the enum schema.
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
