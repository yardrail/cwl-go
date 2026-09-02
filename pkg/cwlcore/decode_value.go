package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The union wrappers.
//
// Every CWL field spelled "a literal or an Expression" decodes the same way: a
// scalar of the literal's kind becomes the literal member, a string becomes the
// expression member, and an absent field leaves the wrapper unset. The unset
// kind is load-bearing rather than incidental — several of these fields have a
// schema default that is not the Go zero value, and one that is unset must not
// be read as zero.

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

// exprLong decodes an `int | long | Expression` field. Both integer members land
// on the same kind: an int64 covers the whole of both ranges, so keeping them
// apart would propagate a distinction with no consumer.
//
// That is also why a [salad.DecimalScalar] falls through to the failure branch
// rather than getting a member of its own. It is by definition outside both
// ranges, the schema validator has already rejected it, and there is no wider
// integer member for it to land on.
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

// resourceValue decodes an `int | long | float | Expression` field, which is
// every field of a ResourceRequirement.
//
// An integer too large for an int64 lands on the float member, which is the only
// member of the union that can hold it and the one the schema validator accepted
// it under. A resource request that large is not satisfiable anyway, so nothing
// is lost by spending its precision here.
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

// unionScalar reads the scalar a literal-or-expression field must hold. It
// reports false when the field is absent, and records an error when the value is
// not a scalar at all.
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

// argument decodes one entry of CommandLineTool.arguments, whose schema type is
// `string | Expression | CommandLineBinding`.
//
// The schema cannot tell its string and Expression members apart — both are
// strings — so decoding separates them by looking for expression syntax in the
// text. A string that embeds none is used verbatim and can never fail to
// evaluate, which is exactly what the plain-string member means.
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

// initialWorkDirListing decodes an InitialWorkDirRequirement's listing, whose
// schema type is `Expression | array<InitialWorkDirEntry>`.
//
// The two forms cannot be flattened into one slice here: when the whole listing
// is an expression, the entries it produces are not known until pkg/cwlexec
// evaluates it.
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

// initialWorkDirEntry decodes one listing entry, whose schema type is
// `null | Dirent | Expression | File | Directory | array<File | Directory>`.
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

// initialWorkDirObject decodes a listing entry written as a mapping: a File or
// Directory value when it declares one of those classes, and a Dirent otherwise.
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
