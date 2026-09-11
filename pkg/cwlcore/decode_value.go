package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Decoding literal-or-Expression union wrappers.

// exprBool decodes a `boolean | Expression` field.
func (d *decoder) exprBool(m *salad.MapNode, key string) ExprBool {
	scalar, ok := d.unionScalar(m, key, "a boolean or an expression")
	if !ok {
		return ExprBool{expr: "", kind: 0, value: false}
	}

	switch scalar.Kind() {
	case salad.BoolScalar:
		return NewExprBool(scalar.AsBool())
	case salad.StringScalar:
		return NewExprBoolExpression(Expression(scalar.String()))
	default:
		d.failUnion(scalar, key, "a boolean or an expression")

		return ExprBool{expr: "", kind: 0, value: false}
	}
}

// exprLong decodes an `int | long | Expression` field.
func (d *decoder) exprLong(m *salad.MapNode, key string) ExprLong {
	scalar, ok := d.unionScalar(m, key, "an integer or an expression")
	if !ok {
		return ExprLong{expr: "", value: 0, kind: 0}
	}

	switch scalar.Kind() {
	case salad.IntScalar:
		number, _ := scalar.AsInt()

		return NewExprLong(number)
	case salad.StringScalar:
		return NewExprLongExpression(Expression(scalar.String()))
	default:
		d.failUnion(scalar, key, "an integer or an expression")

		return ExprLong{expr: "", value: 0, kind: 0}
	}
}

// resourceValue decodes an `int | long | float | Expression` field.
func (d *decoder) resourceValue(m *salad.MapNode, key string) ResourceValue {
	scalar, ok := d.unionScalar(m, key, "a number or an expression")
	if !ok {
		return ResourceValue{expr: "", floatVal: 0, intVal: 0, kind: 0}
	}

	switch scalar.Kind() {
	case salad.IntScalar:
		number, _ := scalar.AsInt()

		return NewResourceInt(number)
	case salad.DecimalScalar, salad.FloatScalar:
		number, _ := scalar.AsFloat()

		return NewResourceFloat(number)
	case salad.StringScalar:
		return NewResourceExpression(Expression(scalar.String()))
	default:
		d.failUnion(scalar, key, "a number or an expression")

		return ResourceValue{expr: "", floatVal: 0, intVal: 0, kind: 0}
	}
}

// unionScalar reads a scalar value from a union field. False if absent.
func (d *decoder) unionScalar(m *salad.MapNode, key, want string) (*salad.ScalarNode, bool) {
	value := fieldNode(m, key)
	if value == nil {
		return nil, false
	}

	scalar, ok := salad.AsScalar(value)
	if !ok {
		d.failf(value.Loc(), "the %q field must be %s, but it is %s", key, want, salad.NodeKind(value))

		return nil, false
	}

	return scalar, true
}

// failUnion records a union member that is a scalar of the wrong kind.
func (d *decoder) failUnion(scalar *salad.ScalarNode, key, want string) {
	d.failf(scalar.Loc(), "the %q field must be %s, but it is %s", key, want, salad.NodeKind(scalar))
}

// argument decodes a `string | Expression | CommandLineBinding` entry.
func (d *decoder) argument(node salad.Node) CommandLineArgument {
	if text, ok := salad.AsString(node); ok {
		if NeedsParsing(text) {
			return NewCommandLineArgumentExpression(Expression(text))
		}

		return NewCommandLineArgumentString(text)
	}

	binding := d.commandLineBinding(node)
	if binding == nil {
		return CommandLineArgument{binding: nil, text: "", kind: 0}
	}

	return NewCommandLineArgumentBinding(binding)
}

// initialWorkDirListing decodes an `Expression | []InitialWorkDirEntry` field.
func (d *decoder) initialWorkDirListing(m *salad.MapNode) InitialWorkDirListing {
	value := fieldNode(m, keyListing)
	if value == nil {
		return InitialWorkDirListing{expr: "", entries: nil, kind: 0}
	}

	if text, ok := salad.AsString(value); ok {
		return NewInitialWorkDirListingExpression(Expression(text))
	}

	seq, ok := salad.AsSeq(value)
	if !ok {
		d.failf(value.Loc(), "the %q field must be an expression or a sequence, but it is %s",
			keyListing, salad.NodeKind(value))

		return InitialWorkDirListing{expr: "", entries: nil, kind: 0}
	}

	return NewInitialWorkDirListing(decodeEach(seq.Items(), d.initialWorkDirEntry))
}

// initialWorkDirEntry decodes one InitialWorkDir listing entry.
func (d *decoder) initialWorkDirEntry(node salad.Node) InitialWorkDirEntry {
	if salad.IsNull(node) {
		return NewInitialWorkDirNull()
	}

	if text, ok := salad.AsString(node); ok {
		return NewInitialWorkDirExpression(Expression(text))
	}

	if seq, ok := salad.AsSeq(node); ok {
		return NewInitialWorkDirObjects(decodeEach(seq.Items(), d.fileOrDirectory))
	}

	m := d.mapping(node, "a listing entry")
	if m == nil {
		return InitialWorkDirEntry{payload: nil, expr: "", kind: 0}
	}

	return d.initialWorkDirObject(m)
}

// initialWorkDirObject decodes a mapping-form listing entry (File, Directory, or Dirent).
func (d *decoder) initialWorkDirObject(m *salad.MapNode) InitialWorkDirEntry {
	switch shortName(lenientText(m, keyClass)) {
	case ClassFile:
		return NewInitialWorkDirFile(d.file(m))
	case ClassDirectory:
		return NewInitialWorkDirDirectory(d.directory(m))
	default:
		return NewInitialWorkDirDirent(d.dirent(m))
	}
}
