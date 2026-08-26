package cwlcore

import (
	"fmt"
	"strconv"
)

// CWL unions, and how this package models them.
//
// The schema is full of fields whose type is a union of a literal and an
// Expression — coresMin is `int | long | float | Expression`, enableReuse is
// `boolean | Expression`, an arguments entry is
// `string | Expression | CommandLineBinding`. Go has no sum types, so every one
// of these is represented the same way, and the uniformity is the point:
// downstream code learns the shape once.
//
// The pattern is a small, comparable-by-construction value type with
// unexported payload fields, a shared ValueKind discriminator, one constructor
// per union member, and one bare accessor per member:
//
//	switch v.Kind() {
//	case cwlcore.ValueExpression:
//	    e := v.Expression()
//	case cwlcore.ValueInt:
//	    n := v.Int()
//	default:
//	    // ValueUnset: the field was absent
//	}
//
// Three rules hold for every wrapper in this file:
//
//   - Kind() is the only discriminator. Accessors are bare, not comma-ok:
//     calling the wrong one returns that member's zero value rather than
//     panicking. Bare accessors keep the shape identical across wrappers,
//     including the boolean ones, where a (bool, bool) result would be both
//     unreadable and rejected by revive's confusing-results rule.
//   - The zero value is ValueUnset, and means "the document did not set this
//     field". That is distinct from an explicitly declared zero — absent is
//     not false, and absent is not 0. Fields whose schema default is not the
//     Go zero value depend on this distinction, so it is load-bearing rather
//     than decorative.
//   - Expression members are stored verbatim and never evaluated here.
//     pkg/cwlexec evaluates them at scheduling time.
//
// Structural records — CommandLineBinding, Dirent, TypeRef's schemas — are
// ordinary structs with exported fields. Only unions are opaque. The dividing
// line is that a union has an invariant worth protecting (the kind must agree
// with the payload) and a plain record does not.
//
// A wrapper is only used where a field genuinely is a union. A plain
// `boolean?` field whose default is false stays a plain bool, because there the
// Go zero value already carries the right meaning; OptBool exists for the two
// places where it does not.

// ValueKind discriminates which member of a union a wrapper value currently
// holds. One enum is shared by every wrapper in this package rather than one
// enum per wrapper, so that a reader who has understood one union has
// understood them all; each wrapper's doc comment names the subset of kinds it
// can actually produce.
type ValueKind uint8

// The union members a wrapper value can hold.
const (
	// ValueUnset is the zero ValueKind: the field was absent from the
	// document. No accessor on the wrapper carries meaning.
	ValueUnset ValueKind = iota

	// ValueNull is an explicit null written in the document, as distinct
	// from an absent field. Only unions whose schema type admits "null" as a
	// member — the InitialWorkDirRequirement listing entries — produce it.
	ValueNull

	// ValueBool is a boolean literal, read with Bool.
	ValueBool

	// ValueInt is an int or long literal, read with Int.
	ValueInt

	// ValueFloat is a float or double literal, read with Float.
	ValueFloat

	// ValueString is a plain string literal that embeds no expression, read
	// with Literal.
	ValueString

	// ValueExpression is an expression-bearing string, read with Expression
	// and left unevaluated by this package.
	ValueExpression

	// ValueBinding is a CommandLineBinding record, read with Binding.
	ValueBinding

	// ValueDirent is a Dirent record, read with Dirent.
	ValueDirent

	// ValueList is a list of union members, read with Entries or Objects.
	ValueList

	// ValueFile is a File value, read with File.
	ValueFile

	// ValueDirectory is a Directory value, read with Directory.
	ValueDirectory
)

// The names the ValueKind and TypeKind String methods render. They are
// constants so that the tests can assert the spellings without restating them,
// and so that the two enums share the one spelling of "unset". Where a name
// coincides with a CWLType symbol it is defined in terms of that constant
// rather than repeating the literal.
const (
	kindNameUnset      = "unset"
	kindNameNull       = PrimitiveNull
	kindNameBool       = "bool"
	kindNameInt        = PrimitiveInt
	kindNameFloat      = PrimitiveFloat
	kindNameString     = PrimitiveString
	kindNameExpression = "expression"
	kindNameBinding    = "binding"
	kindNameDirent     = "dirent"
	kindNameList       = "list"
	kindNameFile       = "file"
	kindNameDirectory  = "directory"
	kindNamePrimitive  = "primitive"
	kindNameRecord     = "record"
	kindNameEnum       = "enum"
	kindNameArray      = "array"
	kindNameUnion      = "union"
	kindNameNamed      = "named"
	kindNameStdin      = "stdin"
	kindNameStdout     = "stdout"
	kindNameStderr     = "stderr"
)

// String renders the ValueKind as the lower-case name of the union member it
// selects, for diagnostics.
func (k ValueKind) String() string {
	names := [...]string{
		kindNameUnset, kindNameNull, kindNameBool, kindNameInt, kindNameFloat,
		kindNameString, kindNameExpression, kindNameBinding, kindNameDirent,
		kindNameList, kindNameFile, kindNameDirectory,
	}
	if int(k) >= len(names) {
		return "ValueKind(" + strconv.Itoa(int(k)) + ")"
	}

	return names[k]
}

// ExprBool is the `boolean | Expression` union. Its kinds are ValueUnset,
// ValueBool and ValueExpression.
//
// It carries WorkReuse.enableReuse, NetworkAccess.networkAccess and
// SecondaryFileSchema.required. Note that enableReuse has a schema default of
// true, so ValueUnset must not be read as false.
type ExprBool struct {
	expr  Expression
	kind  ValueKind
	value bool
}

// NewExprBool returns an ExprBool holding the boolean literal v.
func NewExprBool(v bool) ExprBool {
	return ExprBool{kind: ValueBool, value: v}
}

// NewExprBoolExpression returns an ExprBool holding the unevaluated expression e.
func NewExprBoolExpression(e Expression) ExprBool {
	return ExprBool{kind: ValueExpression, expr: e}
}

// Kind reports which union member this value holds.
func (b ExprBool) Kind() ValueKind {
	return b.kind
}

// IsSet reports whether the document declared this field.
func (b ExprBool) IsSet() bool {
	return b.kind != ValueUnset
}

// Bool returns the boolean literal, or false unless Kind is ValueBool.
func (b ExprBool) Bool() bool {
	return b.value
}

// Expression returns the unevaluated expression, or "" unless Kind is
// ValueExpression.
func (b ExprBool) Expression() Expression {
	return b.expr
}

// String renders the ExprBool for diagnostics.
func (b ExprBool) String() string {
	switch b.kind {
	case ValueBool:
		return strconv.FormatBool(b.value)
	case ValueExpression:
		return string(b.expr)
	default:
		return b.kind.String()
	}
}

// ExprLong is the `int | long | Expression` union. Its kinds are ValueUnset,
// ValueInt and ValueExpression.
//
// It carries ToolTimeLimit.timelimit and CommandLineBinding.position. Both
// schema members, int and long, land on ValueInt: Go's int64 represents the
// whole of both ranges, so keeping them apart would only propagate a
// distinction that has no consumer.
type ExprLong struct {
	expr  Expression
	value int64
	kind  ValueKind
}

// NewExprLong returns an ExprLong holding the integer literal v.
func NewExprLong(v int64) ExprLong {
	return ExprLong{kind: ValueInt, value: v}
}

// NewExprLongExpression returns an ExprLong holding the unevaluated expression e.
func NewExprLongExpression(e Expression) ExprLong {
	return ExprLong{kind: ValueExpression, expr: e}
}

// Kind reports which union member this value holds.
func (l ExprLong) Kind() ValueKind {
	return l.kind
}

// IsSet reports whether the document declared this field.
func (l ExprLong) IsSet() bool {
	return l.kind != ValueUnset
}

// Int returns the integer literal, or 0 unless Kind is ValueInt.
func (l ExprLong) Int() int64 {
	return l.value
}

// Expression returns the unevaluated expression, or "" unless Kind is
// ValueExpression.
func (l ExprLong) Expression() Expression {
	return l.expr
}

// String renders the ExprLong for diagnostics.
func (l ExprLong) String() string {
	switch l.kind {
	case ValueInt:
		return strconv.FormatInt(l.value, 10)
	case ValueExpression:
		return string(l.expr)
	default:
		return l.kind.String()
	}
}

// ResourceValue is the `int | long | float | Expression` union carried by every
// field of ResourceRequirement. Its kinds are ValueUnset, ValueInt, ValueFloat
// and ValueExpression.
//
// ValueUnset is significant here rather than incidental. The schema
// deliberately declines to declare defaults for coresMin, ramMin, tmpdirMin and
// outdirMin, precisely so that an implementation can tell a value that was not
// provided from one that happens to equal the documented default, and apply the
// min/max resolution rules itself. Do not collapse ValueUnset into zero.
type ResourceValue struct {
	expr     Expression
	floatVal float64
	intVal   int64
	kind     ValueKind
}

// NewResourceInt returns a ResourceValue holding the integer literal v, for the
// schema's int and long members.
func NewResourceInt(v int64) ResourceValue {
	return ResourceValue{kind: ValueInt, intVal: v}
}

// NewResourceFloat returns a ResourceValue holding the floating-point literal
// v, for the schema's float member.
func NewResourceFloat(v float64) ResourceValue {
	return ResourceValue{kind: ValueFloat, floatVal: v}
}

// NewResourceExpression returns a ResourceValue holding the unevaluated
// expression e.
func NewResourceExpression(e Expression) ResourceValue {
	return ResourceValue{kind: ValueExpression, expr: e}
}

// Kind reports which union member this value holds.
func (v ResourceValue) Kind() ValueKind {
	return v.kind
}

// IsSet reports whether the document declared this field. A ResourceRequirement
// field that is not set has no default; the resolution rules in the CWL
// ResourceRequirement documentation supply one.
func (v ResourceValue) IsSet() bool {
	return v.kind != ValueUnset
}

// Int returns the integer literal, or 0 unless Kind is ValueInt.
func (v ResourceValue) Int() int64 {
	return v.intVal
}

// Float returns the floating-point literal, or 0 unless Kind is ValueFloat.
func (v ResourceValue) Float() float64 {
	return v.floatVal
}

// Number returns the value as a float64 for either numeric kind, and reports
// whether it held one. It is the accessor most resource arithmetic wants, since
// the min/max rules treat int and float alike.
func (v ResourceValue) Number() (float64, bool) {
	switch v.kind {
	case ValueInt:
		return float64(v.intVal), true
	case ValueFloat:
		return v.floatVal, true
	default:
		return 0, false
	}
}

// Expression returns the unevaluated expression, or "" unless Kind is
// ValueExpression.
func (v ResourceValue) Expression() Expression {
	return v.expr
}

// String renders the ResourceValue for diagnostics.
func (v ResourceValue) String() string {
	switch v.kind {
	case ValueInt:
		return strconv.FormatInt(v.intVal, 10)
	case ValueFloat:
		return strconv.FormatFloat(v.floatVal, 'g', -1, 64)
	case ValueExpression:
		return string(v.expr)
	default:
		return v.kind.String()
	}
}

// CommandLineArgument is one entry of CommandLineTool.arguments: the
// `string | Expression | CommandLineBinding` union. Its kinds are ValueUnset,
// ValueString, ValueExpression and ValueBinding.
//
// The schema's string and Expression members are both strings, and the schema
// alone cannot tell them apart; decoding separates them by inspecting the text
// for expression syntax, so ValueString means "contains no expression" and
// ValueExpression means "does, and must be evaluated before use".
type CommandLineArgument struct {
	binding *CommandLineBinding
	text    string
	kind    ValueKind
}

// NewCommandLineArgumentString returns an argument holding the plain string
// literal s, which embeds no expression.
func NewCommandLineArgumentString(s string) CommandLineArgument {
	return CommandLineArgument{kind: ValueString, text: s}
}

// NewCommandLineArgumentExpression returns an argument holding the unevaluated
// expression e.
func NewCommandLineArgumentExpression(e Expression) CommandLineArgument {
	return CommandLineArgument{kind: ValueExpression, text: string(e)}
}

// NewCommandLineArgumentBinding returns an argument holding the binding b.
func NewCommandLineArgumentBinding(b *CommandLineBinding) CommandLineArgument {
	return CommandLineArgument{kind: ValueBinding, binding: b}
}

// Kind reports which union member this value holds.
func (a CommandLineArgument) Kind() ValueKind {
	return a.kind
}

// Literal returns the plain string literal, or "" unless Kind is ValueString.
func (a CommandLineArgument) Literal() string {
	if a.kind != ValueString {
		return ""
	}

	return a.text
}

// Expression returns the unevaluated expression, or "" unless Kind is
// ValueExpression.
func (a CommandLineArgument) Expression() Expression {
	if a.kind != ValueExpression {
		return ""
	}

	return Expression(a.text)
}

// Binding returns the CommandLineBinding, or nil unless Kind is ValueBinding.
func (a CommandLineArgument) Binding() *CommandLineBinding {
	return a.binding
}

// String renders the CommandLineArgument for diagnostics.
func (a CommandLineArgument) String() string {
	switch a.kind {
	case ValueString, ValueExpression:
		return a.text
	case ValueBinding:
		return fmt.Sprintf("%+v", a.binding)
	default:
		return a.kind.String()
	}
}

// InitialWorkDirEntry is one member of the listing array of an
// InitialWorkDirRequirement: the `null | Dirent | Expression | File | Directory
// | array<File | Directory>` union. Its kinds are ValueUnset, ValueNull,
// ValueDirent, ValueExpression, ValueFile, ValueDirectory and ValueList.
//
// The File and Directory members are the typed values from types_object.go, not
// raw nodes: everything downstream that stages an entry needs a basename, a
// location and a listing, and re-deriving those from a node at staging time
// would put a second, divergent idea of what a File is into pkg/cwlexec.
type InitialWorkDirEntry struct {
	// payload holds the kind-specific member: a *Dirent, a *File, a
	// *Directory, or a []FileOrDirectory for the nested-array member. One
	// field rather than four, on the same reasoning as TypeRef.payload.
	payload any

	// expr is the expression member, kept separate because it is a string
	// rather than a pointer and boxing it would allocate.
	expr Expression

	// kind selects which of the above is meaningful.
	kind ValueKind
}

// NewInitialWorkDirNull returns an entry holding an explicit null, which the
// schema permits and which stages nothing.
func NewInitialWorkDirNull() InitialWorkDirEntry {
	return InitialWorkDirEntry{kind: ValueNull}
}

// NewInitialWorkDirDirent returns an entry holding the Dirent d.
func NewInitialWorkDirDirent(d *Dirent) InitialWorkDirEntry {
	return InitialWorkDirEntry{kind: ValueDirent, payload: d}
}

// NewInitialWorkDirExpression returns an entry holding the unevaluated
// expression e.
func NewInitialWorkDirExpression(e Expression) InitialWorkDirEntry {
	return InitialWorkDirEntry{kind: ValueExpression, expr: e}
}

// NewInitialWorkDirFile returns an entry holding the File value f.
func NewInitialWorkDirFile(f *File) InitialWorkDirEntry {
	return InitialWorkDirEntry{kind: ValueFile, payload: f}
}

// NewInitialWorkDirDirectory returns an entry holding the Directory value d.
func NewInitialWorkDirDirectory(d *Directory) InitialWorkDirEntry {
	return InitialWorkDirEntry{kind: ValueDirectory, payload: d}
}

// NewInitialWorkDirObjects returns an entry holding the nested-array member:
// a list of File and Directory values, in document order.
func NewInitialWorkDirObjects(objects []FileOrDirectory) InitialWorkDirEntry {
	return InitialWorkDirEntry{kind: ValueList, payload: objects}
}

// Kind reports which union member this entry holds.
func (e InitialWorkDirEntry) Kind() ValueKind {
	return e.kind
}

// Dirent returns the Dirent, or nil unless Kind is ValueDirent.
func (e InitialWorkDirEntry) Dirent() *Dirent {
	if dirent, ok := e.payload.(*Dirent); ok {
		return dirent
	}

	return nil
}

// File returns the File value, or nil unless Kind is ValueFile.
func (e InitialWorkDirEntry) File() *File {
	if file, ok := e.payload.(*File); ok {
		return file
	}

	return nil
}

// Directory returns the Directory value, or nil unless Kind is ValueDirectory.
func (e InitialWorkDirEntry) Directory() *Directory {
	if dir, ok := e.payload.(*Directory); ok {
		return dir
	}

	return nil
}

// Objects returns the nested array of File and Directory values, or nil unless
// Kind is ValueList. The returned slice aliases the entry's own storage and
// must not be modified.
func (e InitialWorkDirEntry) Objects() []FileOrDirectory {
	if objects, ok := e.payload.([]FileOrDirectory); ok {
		return objects
	}

	return nil
}

// Expression returns the unevaluated expression, or "" unless Kind is
// ValueExpression.
func (e InitialWorkDirEntry) Expression() Expression {
	return e.expr
}

// String renders the InitialWorkDirEntry for diagnostics.
func (e InitialWorkDirEntry) String() string {
	switch e.kind {
	case ValueExpression:
		return string(e.expr)
	case ValueDirent, ValueFile, ValueDirectory:
		return fmt.Sprintf("%+v", e.payload)
	case ValueList:
		return "[" + strconv.Itoa(len(e.Objects())) + " objects]"
	default:
		return e.kind.String()
	}
}

// InitialWorkDirListing is the listing field of an InitialWorkDirRequirement:
// the `Expression | array<InitialWorkDirEntry>` union. Its kinds are
// ValueUnset, ValueExpression and ValueList.
//
// The whole listing may be a single expression that evaluates to the array, so
// the two forms cannot be flattened into one slice at this layer — the
// expression's result is not known until pkg/cwlexec evaluates it. The schema
// makes listing a required field, so a decoded requirement is never ValueUnset;
// the kind exists because the zero value must still be well defined.
type InitialWorkDirListing struct {
	expr    Expression
	entries []InitialWorkDirEntry
	kind    ValueKind
}

// NewInitialWorkDirListing returns a listing holding the given entries, in
// document order.
func NewInitialWorkDirListing(entries []InitialWorkDirEntry) InitialWorkDirListing {
	return InitialWorkDirListing{kind: ValueList, entries: entries}
}

// NewInitialWorkDirListingExpression returns a listing holding the unevaluated
// expression e, which must evaluate to the listing array.
func NewInitialWorkDirListingExpression(e Expression) InitialWorkDirListing {
	return InitialWorkDirListing{kind: ValueExpression, expr: e}
}

// Kind reports which union member this listing holds.
func (l InitialWorkDirListing) Kind() ValueKind {
	return l.kind
}

// Entries returns the listing entries in document order, or nil unless Kind is
// ValueList. The returned slice aliases the listing's own storage and must not
// be modified.
func (l InitialWorkDirListing) Entries() []InitialWorkDirEntry {
	return l.entries
}

// Expression returns the unevaluated expression, or "" unless Kind is
// ValueExpression.
func (l InitialWorkDirListing) Expression() Expression {
	return l.expr
}

// String renders the InitialWorkDirListing for diagnostics.
func (l InitialWorkDirListing) String() string {
	switch l.kind {
	case ValueExpression:
		return string(l.expr)
	case ValueList:
		return "[" + strconv.Itoa(len(l.entries)) + " entries]"
	default:
		return l.kind.String()
	}
}
