package cwlcore

import (
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// The CWL type language.
//
// A parameter's type is written in the document as one of: a CWLType symbol
// ("File", "int?"), an inline record/enum/array schema, a bare string naming a
// type declared by a SchemaDefRequirement, or a list of any of those meaning a
// union. Schema Salad also expands the `T?` and `T[]` shorthands before
// validation, so `int?` arrives here already as the union [null, int].
//
// TypeRef models that language directly, following the same opaque-union
// pattern as types_value.go: a Kind() discriminator with one accessor per kind.
// The alternative — keeping the raw salad node and re-walking it downstream —
// was rejected: it would push type interpretation into pkg/cwlexec and defeat
// the purpose of having a typed model, and every consumer would have to
// reimplement it. The original node is still retained on every TypeRef, but as
// a diagnostic and extension hook rather than the primary representation.
//
// Names are stored in the short spelling ("File", "int"), not as the IRIs the
// schema's enum symbols use (cwl:File, xsd:int), matching the convention
// salad.PrimitiveType already follows.

// TypeKind discriminates the shape of a TypeRef.
type TypeKind uint8

// The shapes a TypeRef can take.
const (
	// TypeKindUnset is the zero TypeKind: no type was decoded. A parameter's
	// type is required by the schema, so a decoded parameter never has one.
	TypeKindUnset TypeKind = iota

	// TypeKindPrimitive is a CWLType symbol, read with Name and compared
	// against the Primitive* constants.
	TypeKindPrimitive

	// TypeKindRecord is an inline record schema, read with Record.
	TypeKindRecord

	// TypeKindEnum is an inline enum schema, read with Enum.
	TypeKindEnum

	// TypeKindArray is an inline array schema, read with Array.
	TypeKindArray

	// TypeKindUnion is a list of alternative types, read with Options. This
	// is what the `T?` shorthand expands to, as [null, T].
	TypeKindUnion

	// TypeKindNamed is a string reference to a named type — one declared by
	// a SchemaDefRequirement, or the identifier of a schema declared
	// elsewhere in the document. Read the reference with Name. Resolving it
	// requires the requirement scope and so is not done here.
	TypeKindNamed

	// TypeKindStdin is the `stdin` type shortcut, valid only on a
	// CommandInputParameter. It declares a File input wired to the tool's
	// standard input.
	TypeKindStdin

	// TypeKindStdout is the `stdout` type shortcut, valid only on a
	// CommandOutputParameter. It declares a File output capturing the tool's
	// standard output.
	TypeKindStdout

	// TypeKindStderr is the `stderr` type shortcut, valid only on a
	// CommandOutputParameter. It declares a File output capturing the tool's
	// standard error.
	TypeKindStderr
)

// String renders the TypeKind as the lower-case name of the shape it selects.
func (k TypeKind) String() string {
	names := [...]string{
		kindNameUnset, kindNamePrimitive, kindNameRecord, kindNameEnum, kindNameArray,
		kindNameUnion, kindNameNamed, kindNameStdin, kindNameStdout, kindNameStderr,
	}
	if int(k) >= len(names) {
		return "TypeKind(?)"
	}

	return names[k]
}

// The CWLType symbols, in the short spelling TypeRef.Name uses. The first seven
// come from the Schema Salad PrimitiveType enum; File and Directory are CWL's
// additions; Any is the salad wildcard.
const (
	// PrimitiveNull is the null type, the type of a value that is absent.
	PrimitiveNull = "null"

	// PrimitiveBoolean is the boolean type.
	PrimitiveBoolean = "boolean"

	// PrimitiveInt is the 32-bit signed integer type.
	PrimitiveInt = "int"

	// PrimitiveLong is the 64-bit signed integer type.
	PrimitiveLong = "long"

	// PrimitiveFloat is the single-precision floating point type.
	PrimitiveFloat = "float"

	// PrimitiveDouble is the double-precision floating point type.
	PrimitiveDouble = "double"

	// PrimitiveString is the Unicode character sequence type.
	PrimitiveString = "string"

	// PrimitiveFile is the File type, a CWL addition to the salad primitives.
	PrimitiveFile = "File"

	// PrimitiveDirectory is the Directory type, a CWL addition to the salad
	// primitives.
	PrimitiveDirectory = "Directory"

	// PrimitiveAny is the wildcard type, matching any non-null value.
	PrimitiveAny = "Any"
)

// TypeRef is a resolved CWL type expression. The zero value is TypeKindUnset,
// meaning no type was decoded.
//
// Construct one with the New*Type functions, which keep the kind and payload in
// agreement, and read it by switching on Kind:
//
//	switch t.Kind() {
//	case cwlcore.TypeKindArray:
//	    item := t.Array().Items
//	case cwlcore.TypeKindUnion:
//	    for _, opt := range t.Options() { /* ... */ }
//	}
type TypeRef struct {
	// payload holds the kind-specific member: a *RecordSchema, *EnumSchema
	// or *ArraySchema for the inline schema kinds, or a []TypeRef for a
	// union. It is one field rather than four because a TypeRef is copied by
	// value everywhere in the model — it is a field of ParameterBase and of
	// every RecordField — and four mutually exclusive members would make
	// every copy pay for all of them.
	payload any

	// node is the validated salad node this type was decoded from, if one
	// was attached.
	node salad.Node

	// name is the CWLType symbol for a primitive, or the reference for a
	// named type. Empty for every other kind.
	name string

	// kind selects which of the above is meaningful.
	kind TypeKind
}

// NewPrimitiveType returns a TypeRef naming the CWLType symbol name, which
// should be one of the Primitive constants.
func NewPrimitiveType(name string) TypeRef {
	return TypeRef{payload: nil, node: nil, name: name, kind: TypeKindPrimitive}
}

// NewNamedType returns a TypeRef referring to the named type name, typically
// one declared by a SchemaDefRequirement. The reference is not resolved.
func NewNamedType(name string) TypeRef {
	return TypeRef{payload: nil, node: nil, name: name, kind: TypeKindNamed}
}

// NewRecordType returns a TypeRef holding the inline record schema s.
func NewRecordType(s *RecordSchema) TypeRef {
	return TypeRef{payload: s, node: nil, name: "", kind: TypeKindRecord}
}

// NewEnumType returns a TypeRef holding the inline enum schema s.
func NewEnumType(s *EnumSchema) TypeRef {
	return TypeRef{payload: s, node: nil, name: "", kind: TypeKindEnum}
}

// NewArrayType returns a TypeRef holding the inline array schema s.
func NewArrayType(s *ArraySchema) TypeRef {
	return TypeRef{payload: s, node: nil, name: "", kind: TypeKindArray}
}

// NewUnionType returns a TypeRef holding the alternative types options, in
// document order. Order is preserved because CWL resolves a value against a
// union by trying its members in order.
func NewUnionType(options []TypeRef) TypeRef {
	return TypeRef{payload: options, node: nil, name: "", kind: TypeKindUnion}
}

// NewShortcutType returns a TypeRef for one of the stdin, stdout and stderr
// type shortcuts. kind must be TypeKindStdin, TypeKindStdout or
// TypeKindStderr; any other kind yields the zero TypeRef.
func NewShortcutType(kind TypeKind) TypeRef {
	switch kind {
	case TypeKindStdin, TypeKindStdout, TypeKindStderr:
		return TypeRef{payload: nil, node: nil, name: "", kind: kind}
	default:
		return TypeRef{payload: nil, node: nil, name: "", kind: 0}
	}
}

// WithNode returns a copy of t carrying n as the validated salad node it was
// decoded from. It is separate from the constructors so that they stay within
// a single argument.
func (t TypeRef) WithNode(n salad.Node) TypeRef {
	t.node = n

	return t
}

// Kind reports the shape of this type expression.
func (t TypeRef) Kind() TypeKind {
	return t.kind
}

// IsSet reports whether a type was decoded at all.
func (t TypeRef) IsSet() bool {
	return t.kind != TypeKindUnset
}

// Name returns the CWLType symbol for TypeKindPrimitive, or the type reference
// for TypeKindNamed. It returns "" for every other kind.
func (t TypeRef) Name() string {
	return t.name
}

// Record returns the inline record schema, or nil unless Kind is
// TypeKindRecord.
func (t TypeRef) Record() *RecordSchema {
	if schema, ok := t.payload.(*RecordSchema); ok {
		return schema
	}

	return nil
}

// Enum returns the inline enum schema, or nil unless Kind is TypeKindEnum.
func (t TypeRef) Enum() *EnumSchema {
	if schema, ok := t.payload.(*EnumSchema); ok {
		return schema
	}

	return nil
}

// Array returns the inline array schema, or nil unless Kind is TypeKindArray.
func (t TypeRef) Array() *ArraySchema {
	if schema, ok := t.payload.(*ArraySchema); ok {
		return schema
	}

	return nil
}

// Options returns the union's alternative types in document order, or nil
// unless Kind is TypeKindUnion. The returned slice aliases the TypeRef's own
// storage and must not be modified.
func (t TypeRef) Options() []TypeRef {
	if options, ok := t.payload.([]TypeRef); ok {
		return options
	}

	return nil
}

// Node returns the validated salad node this type was decoded from, or nil if
// none was attached. Use it for diagnostics and for extension fields this
// package does not model, never to re-derive information the typed accessors
// already give.
func (t TypeRef) Node() salad.Node {
	return t.node
}

// IsNull reports whether this type is exactly the null primitive.
func (t TypeRef) IsNull() bool {
	return t.kind == TypeKindPrimitive && t.name == PrimitiveNull
}

// IsOptional reports whether a value of this type may be null: that is, whether
// it is a union with a null member. `T?` expands to such a union before
// decoding, so this is how optionality is tested.
func (t TypeRef) IsOptional() bool {
	if t.kind != TypeKindUnion {
		return false
	}

	for _, opt := range t.Options() {
		if opt.IsNull() {
			return true
		}
	}

	return false
}

// String renders the type expression in an approximation of CWL's own syntax,
// for diagnostics. It is not a faithful serialization and must not be parsed.
func (t TypeRef) String() string {
	switch t.kind {
	case TypeKindPrimitive, TypeKindNamed:
		return t.name
	case TypeKindArray:
		return t.arrayString()
	case TypeKindUnion:
		return t.unionString()
	default:
		// TypeKindRecord and TypeKindEnum render as their kind name, as
		// do the stdin/stdout/stderr shortcuts and the unset zero value.
		return t.kind.String()
	}
}

// arrayString renders an array type as "items[]".
func (t TypeRef) arrayString() string {
	schema := t.Array()
	if schema == nil {
		return "[]"
	}

	return schema.Items.String() + "[]"
}

// unionString renders a union type as "a|b|c".
func (t TypeRef) unionString() string {
	options := t.Options()

	parts := make([]string, 0, len(options))
	for _, opt := range options {
		parts = append(parts, opt.String())
	}

	return strings.Join(parts, "|")
}

// RecordSchema is an inline record type: a named, ordered set of fields.
//
// It flattens the schema's Input/Output/CommandInput/CommandOutput record
// schema records into one Go type. InputBinding is only ever populated for an
// input-side schema, where the schema extends CommandLineBindable.
type RecordSchema struct {
	// Node is the validated salad node this schema was decoded from.
	Node salad.Node

	// InputBinding describes how a value of this type is turned into
	// command-line arguments. Input-side schemas only; nil elsewhere.
	InputBinding *CommandLineBinding

	// Name is the schema's identifier, resolved to an absolute identifier
	// when the document gave one, and empty for an anonymous inline schema.
	Name string

	// Label is a short human-readable label.
	Label string

	// Doc is the documentation string, normalized from the schema's
	// `string | string[]` form.
	Doc []string

	// Fields are the record's fields, in document order. Order matters: it
	// determines the order of the command-line arguments a record input
	// produces.
	Fields []RecordField
}

// RecordField is one field of a RecordSchema.
//
// Like RecordSchema it flattens the four schema variants into one Go type, so
// it carries both an InputBinding and an OutputBinding; at most one is ever
// populated, according to which side of the process the enclosing schema is on.
// Its field set is otherwise the same as ParameterBase's, minus the identifier,
// because the schema builds both from the same abstract FieldBase record.
type RecordField struct {
	// Node is the validated salad node this field was decoded from.
	Node salad.Node

	// InputBinding describes how this field's value becomes command-line
	// arguments. Input-side fields only; nil elsewhere.
	InputBinding *CommandLineBinding

	// OutputBinding describes how this field's value is collected from the
	// output directory. Output-side fields only; nil elsewhere.
	OutputBinding *CommandOutputBinding

	// Name is the field's name, resolved to an absolute identifier.
	Name string

	// Label is a short human-readable label.
	Label string

	// LoadListing is how deeply a Directory value's listing is populated.
	// Input-side fields only.
	LoadListing LoadListingEnum

	// Doc is the documentation string, normalized to a slice.
	Doc []string

	// Type is the field's type.
	Type TypeRef

	// SecondaryFiles declares files that must accompany a File value.
	SecondaryFiles []SecondaryFileSchema

	// Format constrains or declares the media type of a File value. On the
	// input side the schema allows a list; on the output side at most one
	// entry is ever present.
	Format []Expression

	// LoadContents requests that a File value's contents be read into its
	// contents field. Input-side fields only.
	LoadContents bool

	// Streamable declares that a File value may be a named pipe rather than
	// a seekable file.
	Streamable bool
}

// EnumSchema is an inline enum type: a named, ordered set of symbols.
//
// It flattens the schema's Input/Output/CommandInput/CommandOutput enum schema
// records into one Go type, on the same terms as RecordSchema.
type EnumSchema struct {
	// Node is the validated salad node this schema was decoded from.
	Node salad.Node

	// InputBinding describes how a value of this type is turned into
	// command-line arguments. Input-side schemas only; nil elsewhere.
	InputBinding *CommandLineBinding

	// Name is the schema's identifier, resolved to an absolute identifier
	// when the document gave one, and empty for an anonymous inline schema.
	Name string

	// Label is a short human-readable label.
	Label string

	// Doc is the documentation string, normalized to a slice.
	Doc []string

	// Symbols are the permitted values, in document order and resolved to
	// absolute identifiers.
	Symbols []string
}

// ArraySchema is an inline array type.
//
// It flattens the schema's Input/Output/CommandInput/CommandOutput array schema
// records into one Go type, on the same terms as RecordSchema.
type ArraySchema struct {
	// Node is the validated salad node this schema was decoded from.
	Node salad.Node

	// InputBinding describes how a value of this type is turned into
	// command-line arguments. Input-side schemas only; nil elsewhere.
	InputBinding *CommandLineBinding

	// Name is the schema's identifier, resolved to an absolute identifier
	// when the document gave one, and empty for an anonymous inline schema.
	Name string

	// Label is a short human-readable label.
	Label string

	// Doc is the documentation string, normalized to a slice.
	Doc []string

	// Items is the type of the array's elements.
	Items TypeRef
}
