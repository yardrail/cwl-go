package cwlcore

import (
	"fmt"
	"strconv"
)

// Union wrapper types for CWL fields. Each wraps a discriminated value
// (Kind + typed accessors). Zero value is ValueUnset (field absent).

// ValueKind discriminates which member of a union wrapper is set.
type ValueKind uint8

const (
	// ValueUnset is the zero value: no value set.
	ValueUnset      ValueKind = iota
	ValueNull                 // explicit null
	ValueBool                 // boolean literal
	ValueInt                  // int or long literal
	ValueFloat                // float or double literal
	ValueString               // plain string, no expression
	ValueExpression           // expression-bearing string
	ValueBinding              // CommandLineBinding
	ValueDirent               // Dirent
	ValueList                 // list of union members
	ValueFile                 // File value
	ValueDirectory            // Directory value
)

// Kind name constants shared by ValueKind and TypeKind String methods.
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

// String renders the ValueKind for diagnostics.
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

// ExprBool is the `boolean | Expression` union.
type ExprBool struct {
	expr  Expression
	kind  ValueKind
	value bool
}

// NewExprBool wraps a boolean literal.
func NewExprBool(v bool) ExprBool {
	return ExprBool{expr: "", kind: ValueBool, value: v}
}

// NewExprBoolExpression wraps an unevaluated expression.
func NewExprBoolExpression(e Expression) ExprBool {
	return ExprBool{expr: e, kind: ValueExpression, value: false}
}

// Kind reports the active union member.
func (b ExprBool) Kind() ValueKind {
	return b.kind
}

// IsSet reports whether the field was declared.
func (b ExprBool) IsSet() bool {
	return b.kind != ValueUnset
}

// Bool returns the boolean literal, or false if not ValueBool.
func (b ExprBool) Bool() bool {
	return b.value
}

// Expression returns the unevaluated expression, or "" if not ValueExpression.
func (b ExprBool) Expression() Expression {
	return b.expr
}

// String renders for diagnostics.
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

// ExprLong is the `int | long | Expression` union.
type ExprLong struct {
	expr  Expression
	value int64
	kind  ValueKind
}

// NewExprLong wraps an integer literal.
func NewExprLong(v int64) ExprLong {
	return ExprLong{expr: "", value: v, kind: ValueInt}
}

// NewExprLongExpression wraps an unevaluated expression.
func NewExprLongExpression(e Expression) ExprLong {
	return ExprLong{expr: e, value: 0, kind: ValueExpression}
}

// Kind reports the active union member.
func (l ExprLong) Kind() ValueKind {
	return l.kind
}

// IsSet reports whether the field was declared.
func (l ExprLong) IsSet() bool {
	return l.kind != ValueUnset
}

// Int returns the integer literal, or 0 if not ValueInt.
func (l ExprLong) Int() int64 {
	return l.value
}

// Expression returns the unevaluated expression, or "" if not ValueExpression.
func (l ExprLong) Expression() Expression {
	return l.expr
}

// String renders for diagnostics.
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

// ResourceValue is the `int | long | float | Expression` union for ResourceRequirement fields.
type ResourceValue struct {
	expr     Expression
	floatVal float64
	intVal   int64
	kind     ValueKind
}

// NewResourceInt wraps an integer literal.
func NewResourceInt(v int64) ResourceValue {
	return ResourceValue{expr: "", floatVal: 0, intVal: v, kind: ValueInt}
}

// NewResourceFloat wraps a float literal.
func NewResourceFloat(v float64) ResourceValue {
	return ResourceValue{expr: "", floatVal: v, intVal: 0, kind: ValueFloat}
}

// NewResourceExpression wraps an unevaluated expression.
func NewResourceExpression(e Expression) ResourceValue {
	return ResourceValue{expr: e, floatVal: 0, intVal: 0, kind: ValueExpression}
}

// Kind reports the active union member.
func (v ResourceValue) Kind() ValueKind {
	return v.kind
}

// IsSet reports whether the field was declared.
func (v ResourceValue) IsSet() bool {
	return v.kind != ValueUnset
}

// Int returns the integer literal, or 0 if not ValueInt.
func (v ResourceValue) Int() int64 {
	return v.intVal
}

// Float returns the float literal, or 0 if not ValueFloat.
func (v ResourceValue) Float() float64 {
	return v.floatVal
}

// Number returns the value as float64 for either numeric kind.
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

// Expression returns the unevaluated expression, or "" if not ValueExpression.
func (v ResourceValue) Expression() Expression {
	return v.expr
}

// String renders for diagnostics.
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

// CommandLineArgument is the `string | Expression | CommandLineBinding` union.
type CommandLineArgument struct {
	binding *CommandLineBinding
	text    string
	kind    ValueKind
}

// NewCommandLineArgumentString wraps a plain string literal.
func NewCommandLineArgumentString(s string) CommandLineArgument {
	return CommandLineArgument{binding: nil, text: s, kind: ValueString}
}

// NewCommandLineArgumentExpression wraps an unevaluated expression.
func NewCommandLineArgumentExpression(e Expression) CommandLineArgument {
	return CommandLineArgument{binding: nil, text: string(e), kind: ValueExpression}
}

// NewCommandLineArgumentBinding wraps a CommandLineBinding.
func NewCommandLineArgumentBinding(b *CommandLineBinding) CommandLineArgument {
	return CommandLineArgument{binding: b, text: "", kind: ValueBinding}
}

// Kind reports the active union member.
func (a CommandLineArgument) Kind() ValueKind {
	return a.kind
}

// Literal returns the plain string literal, or "" if not ValueString.
func (a CommandLineArgument) Literal() string {
	if a.kind != ValueString {
		return ""
	}

	return a.text
}

// Expression returns the unevaluated expression, or "" if not ValueExpression.
func (a CommandLineArgument) Expression() Expression {
	if a.kind != ValueExpression {
		return ""
	}

	return Expression(a.text)
}

// Binding returns the CommandLineBinding, or nil if not ValueBinding.
func (a CommandLineArgument) Binding() *CommandLineBinding {
	return a.binding
}

// String renders for diagnostics.
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

// InitialWorkDirEntry is the `null | Dirent | Expression | File | Directory | []FileOrDirectory` union.
type InitialWorkDirEntry struct {
	payload any
	expr    Expression
	kind    ValueKind
}

// NewInitialWorkDirNull wraps an explicit null.
func NewInitialWorkDirNull() InitialWorkDirEntry {
	return InitialWorkDirEntry{payload: nil, expr: "", kind: ValueNull}
}

// NewInitialWorkDirDirent wraps a Dirent.
func NewInitialWorkDirDirent(d *Dirent) InitialWorkDirEntry {
	return InitialWorkDirEntry{payload: d, expr: "", kind: ValueDirent}
}

// NewInitialWorkDirExpression wraps an unevaluated expression.
func NewInitialWorkDirExpression(e Expression) InitialWorkDirEntry {
	return InitialWorkDirEntry{payload: nil, expr: e, kind: ValueExpression}
}

// NewInitialWorkDirFile wraps a File.
func NewInitialWorkDirFile(f *File) InitialWorkDirEntry {
	return InitialWorkDirEntry{payload: f, expr: "", kind: ValueFile}
}

// NewInitialWorkDirDirectory wraps a Directory.
func NewInitialWorkDirDirectory(d *Directory) InitialWorkDirEntry {
	return InitialWorkDirEntry{payload: d, expr: "", kind: ValueDirectory}
}

// NewInitialWorkDirObjects wraps a list of File and Directory values.
func NewInitialWorkDirObjects(objects []FileOrDirectory) InitialWorkDirEntry {
	return InitialWorkDirEntry{payload: objects, expr: "", kind: ValueList}
}

// Kind reports the active union member.
func (e InitialWorkDirEntry) Kind() ValueKind {
	return e.kind
}

// Dirent returns the Dirent, or nil if not ValueDirent.
func (e InitialWorkDirEntry) Dirent() *Dirent {
	if dirent, ok := e.payload.(*Dirent); ok {
		return dirent
	}

	return nil
}

// File returns the File, or nil if not ValueFile.
func (e InitialWorkDirEntry) File() *File {
	if file, ok := e.payload.(*File); ok {
		return file
	}

	return nil
}

// Directory returns the Directory, or nil if not ValueDirectory.
func (e InitialWorkDirEntry) Directory() *Directory {
	if dir, ok := e.payload.(*Directory); ok {
		return dir
	}

	return nil
}

// Objects returns the File/Directory list, or nil if not ValueList.
func (e InitialWorkDirEntry) Objects() []FileOrDirectory {
	if objects, ok := e.payload.([]FileOrDirectory); ok {
		return objects
	}

	return nil
}

// Expression returns the unevaluated expression, or "" if not ValueExpression.
func (e InitialWorkDirEntry) Expression() Expression {
	return e.expr
}

// String renders for diagnostics.
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

// InitialWorkDirListing is the `Expression | []InitialWorkDirEntry` union.
type InitialWorkDirListing struct {
	expr    Expression
	entries []InitialWorkDirEntry
	kind    ValueKind
}

// NewInitialWorkDirListing wraps a list of entries.
func NewInitialWorkDirListing(entries []InitialWorkDirEntry) InitialWorkDirListing {
	return InitialWorkDirListing{expr: "", entries: entries, kind: ValueList}
}

// NewInitialWorkDirListingExpression wraps an unevaluated expression.
func NewInitialWorkDirListingExpression(e Expression) InitialWorkDirListing {
	return InitialWorkDirListing{expr: e, entries: nil, kind: ValueExpression}
}

// Kind reports the active union member.
func (l InitialWorkDirListing) Kind() ValueKind {
	return l.kind
}

// Entries returns the listing entries, or nil if not ValueList.
func (l InitialWorkDirListing) Entries() []InitialWorkDirEntry {
	return l.entries
}

// Expression returns the unevaluated expression, or "" if not ValueExpression.
func (l InitialWorkDirListing) Expression() Expression {
	return l.expr
}

// String renders for diagnostics.
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
