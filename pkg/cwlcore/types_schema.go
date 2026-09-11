package cwlcore

import (
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// CWL type language. TypeRef models primitives, inline schemas, named types, and unions.

// TypeKind discriminates the shape of a [TypeRef].
type TypeKind uint8

const (
	TypeKindUnset     TypeKind = iota
	TypeKindPrimitive          // CWLType symbol
	TypeKindRecord             // inline record schema
	TypeKindEnum               // inline enum schema
	TypeKindArray              // inline array schema
	TypeKindUnion              // list of alternatives (T? expands to [null, T])
	TypeKindNamed              // reference to a named type (SchemaDefRequirement etc.)
	TypeKindStdin              // stdin type shortcut
	TypeKindStdout             // stdout type shortcut
	TypeKindStderr             // stderr type shortcut
)

// String renders for diagnostics.
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

// CWLType symbols.
const (
	PrimitiveNull      = "null"
	PrimitiveBoolean   = "boolean"
	PrimitiveInt       = "int"
	PrimitiveLong      = "long"
	PrimitiveFloat     = "float"
	PrimitiveDouble    = "double"
	PrimitiveString    = "string"
	PrimitiveFile      = "File"
	PrimitiveDirectory = "Directory"
	PrimitiveAny       = "Any"
)

// TypeRef is a resolved CWL type expression. Zero value is TypeKindUnset.
type TypeRef struct {
	payload any
	node    salad.Node
	name    string
	kind    TypeKind
}

// NewPrimitiveType wraps a CWLType symbol.
func NewPrimitiveType(name string) TypeRef {
	return TypeRef{payload: nil, node: nil, name: name, kind: TypeKindPrimitive}
}

// NewNamedType wraps a named type reference (unresolved).
func NewNamedType(name string) TypeRef {
	return TypeRef{payload: nil, node: nil, name: name, kind: TypeKindNamed}
}

// NewRecordType wraps an inline record schema.
func NewRecordType(s *RecordSchema) TypeRef {
	return TypeRef{payload: s, node: nil, name: "", kind: TypeKindRecord}
}

// NewEnumType wraps an inline enum schema.
func NewEnumType(s *EnumSchema) TypeRef {
	return TypeRef{payload: s, node: nil, name: "", kind: TypeKindEnum}
}

// NewArrayType wraps an inline array schema.
func NewArrayType(s *ArraySchema) TypeRef {
	return TypeRef{payload: s, node: nil, name: "", kind: TypeKindArray}
}

// NewUnionType wraps a list of alternative types.
func NewUnionType(options []TypeRef) TypeRef {
	return TypeRef{payload: options, node: nil, name: "", kind: TypeKindUnion}
}

// NewShortcutType wraps a stdin/stdout/stderr type shortcut.
func NewShortcutType(kind TypeKind) TypeRef {
	switch kind {
	case TypeKindStdin, TypeKindStdout, TypeKindStderr:
		return TypeRef{payload: nil, node: nil, name: "", kind: kind}
	default:
		return TypeRef{payload: nil, node: nil, name: "", kind: 0}
	}
}

// WithNode returns a copy of t carrying the source salad node.
func (t TypeRef) WithNode(n salad.Node) TypeRef {
	t.node = n

	return t
}

// Kind reports the type shape.
func (t TypeRef) Kind() TypeKind {
	return t.kind
}

// IsSet reports whether a type was decoded.
func (t TypeRef) IsSet() bool {
	return t.kind != TypeKindUnset
}

// Name returns the CWLType symbol or named reference, or "" for other kinds.
func (t TypeRef) Name() string {
	return t.name
}

// Record returns the inline record schema, or nil if not TypeKindRecord.
func (t TypeRef) Record() *RecordSchema {
	if schema, ok := t.payload.(*RecordSchema); ok {
		return schema
	}

	return nil
}

// Enum returns the inline enum schema, or nil if not TypeKindEnum.
func (t TypeRef) Enum() *EnumSchema {
	if schema, ok := t.payload.(*EnumSchema); ok {
		return schema
	}

	return nil
}

// Array returns the inline array schema, or nil if not TypeKindArray.
func (t TypeRef) Array() *ArraySchema {
	if schema, ok := t.payload.(*ArraySchema); ok {
		return schema
	}

	return nil
}

// Options returns the union's alternatives, or nil if not TypeKindUnion.
func (t TypeRef) Options() []TypeRef {
	if options, ok := t.payload.([]TypeRef); ok {
		return options
	}

	return nil
}

// Node returns the source salad node, or nil.
func (t TypeRef) Node() salad.Node {
	return t.node
}

// IsNull reports whether this is the null primitive type.
func (t TypeRef) IsNull() bool {
	return t.kind == TypeKindPrimitive && t.name == PrimitiveNull
}

// IsOptional reports whether this is a union containing null.
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

// String renders for diagnostics.
func (t TypeRef) String() string {
	switch t.kind {
	case TypeKindPrimitive, TypeKindNamed:
		return t.name
	case TypeKindArray:
		return t.arrayString()
	case TypeKindUnion:
		return t.unionString()
	default:
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
