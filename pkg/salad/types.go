package salad

import "strings"

// Type is a sealed union of *RecordType, *EnumType, *ArrayType, *MapType, *UnionType and *PrimitiveType.
type Type interface {
	// TypeName returns the fully-qualified name of the type, or "" if it is anonymous.
	TypeName() string
	isType()
}

// PrimitiveKind identifies one of Schema Salad's primitive types.
type PrimitiveKind int

const (
	// PrimitiveNull is the null type, the only value of which is null.
	PrimitiveNull PrimitiveKind = iota
	// PrimitiveBoolean is a binary value.
	PrimitiveBoolean
	// PrimitiveInt is a 32-bit signed integer.
	PrimitiveInt
	// PrimitiveLong is a 64-bit signed integer.
	PrimitiveLong
	// PrimitiveFloat is a single-precision (32-bit) IEEE 754 floating-point number.
	PrimitiveFloat
	// PrimitiveDouble is a double-precision (64-bit) IEEE 754 floating-point number.
	PrimitiveDouble
	// PrimitiveString is a Unicode character sequence.
	PrimitiveString
	// PrimitiveAny is Schema Salad's Any: any non-null value.
	PrimitiveAny
)

// primitiveKindNames maps each PrimitiveKind to its Schema Salad type name.
var primitiveKindNames = [...]string{
	PrimitiveNull:    nameNull,
	PrimitiveBoolean: nameBoolean,
	PrimitiveInt:     nameInt,
	PrimitiveLong:    nameLong,
	PrimitiveFloat:   nameFloat,
	PrimitiveDouble:  nameDouble,
	PrimitiveString:  nameString,
	PrimitiveAny:     nameAny,
}

// primitiveKindsByName is the reverse of primitiveKindNames.
var primitiveKindsByName = buildPrimitiveKindIndex()

// primitiveTypes holds one shared immutable *PrimitiveType per kind.
var primitiveTypes = buildPrimitiveTypes()

func buildPrimitiveKindIndex() map[string]PrimitiveKind {
	out := make(map[string]PrimitiveKind, len(primitiveKindNames))
	for i, name := range primitiveKindNames {
		out[name] = PrimitiveKind(i)
	}

	return out
}

func buildPrimitiveTypes() []*PrimitiveType {
	out := make([]*PrimitiveType, 0, len(primitiveKindNames))
	for i := range primitiveKindNames {
		out = append(out, &PrimitiveType{Kind: PrimitiveKind(i)})
	}

	return out
}

// String returns the Schema Salad name of the primitive kind, as it is spelled in
// a schema document.
func (k PrimitiveKind) String() string {
	if k < 0 || int(k) >= len(primitiveKindNames) {
		return nameUnknown
	}

	return primitiveKindNames[k]
}

// PrimitiveKindOf maps a Schema Salad primitive type name to its kind, and
// reports whether the name is a primitive at all.
func PrimitiveKindOf(name string) (PrimitiveKind, bool) {
	k, ok := primitiveKindsByName[name]

	return k, ok
}

// Primitive returns the shared immutable *PrimitiveType for a kind.
func Primitive(k PrimitiveKind) *PrimitiveType {
	if k < 0 || int(k) >= len(primitiveTypes) {
		return &PrimitiveType{Kind: k}
	}

	return primitiveTypes[k]
}

// PrimitiveType is a Schema Salad primitive (null, boolean, int, long, float, double, string, Any).
type PrimitiveType struct {
	// Kind identifies which primitive this is.
	Kind PrimitiveKind
}

var _ Type = (*PrimitiveType)(nil)

// TypeName returns the Schema Salad name of the primitive, such as "int".
func (p *PrimitiveType) TypeName() string {
	return p.Kind.String()
}

func (p *PrimitiveType) isType() {}

// ArrayType is a homogeneous list. It is always anonymous.
type ArrayType struct {
	// Items is the type of every element.
	Items Type
}

var _ Type = (*ArrayType)(nil)

// TypeName returns "": array types are anonymous.
func (a *ArrayType) TypeName() string {
	return ""
}

func (a *ArrayType) isType() {}

// MapType is a string-keyed map with homogeneous values. It is always anonymous.
type MapType struct {
	// Values is the type of every value in the map.
	Values Type
}

var _ Type = (*MapType)(nil)

// TypeName returns "": map types are anonymous.
func (m *MapType) TypeName() string {
	return ""
}

func (m *MapType) isType() {}

// UnionType is a set of alternative types (always anonymous).
type UnionType struct {
	// Options are the alternatives, in declaration order.
	Options []Type
}

var _ Type = (*UnionType)(nil)

// TypeName returns "": union types are anonymous.
func (u *UnionType) TypeName() string {
	return ""
}

// HasNull reports whether null is one of the alternatives.
func (u *UnionType) HasNull() bool {
	if u == nil {
		return false
	}

	for _, opt := range u.Options {
		if p, ok := opt.(*PrimitiveType); ok && p.Kind == PrimitiveNull {
			return true
		}
	}

	return false
}

func (u *UnionType) isType() {}

// EnumType is a closed set of symbols.
type EnumType struct {
	// Name is the fully-qualified type name.
	Name string
	// Symbols are the permitted values as fully-qualified IRIs.
	Symbols []string
	// Doc is the type's documentation, one entry per doc: string.
	Doc []string
	// Extends names the base types this enum was flattened from.
	Extends []string
}

var _ Type = (*EnumType)(nil)

// TypeName returns the fully-qualified name of the enum.
func (e *EnumType) TypeName() string {
	if e == nil {
		return ""
	}

	return e.Name
}

// HasSymbol reports whether sym matches any symbol by IRI or short name.
func (e *EnumType) HasSymbol(sym string) bool {
	if e == nil {
		return false
	}

	short := shortName(sym)
	for _, s := range e.Symbols {
		if s == sym || shortName(s) == short {
			return true
		}
	}

	return false
}

func (e *EnumType) isType() {}

// narrowsFieldOverride reports whether e declares a symbol matching recordName's short name.
func (e *EnumType) narrowsFieldOverride(recordName string) bool {
	return e.HasSymbol(shortName(recordName))
}

// Field is one field of a RecordType.
type Field struct {
	Type          Type
	Default       Node
	JSONLDPred    *TermDef
	Name          string
	InheritedFrom string
	Doc           []string
	Optional      bool
}

// ShortName returns the trailing short name of the field's identifier.
func (f *Field) ShortName() string {
	if f == nil {
		return ""
	}

	return shortName(f.Name)
}

// RecordType is a record with ordered fields.
type RecordType struct {
	Name         string
	Fields       []*Field
	Doc          []string
	Extends      []string
	DocumentRoot bool
	Abstract     bool
}

var _ Type = (*RecordType)(nil)

// TypeName returns the fully-qualified name of the record.
func (r *RecordType) TypeName() string {
	if r == nil {
		return ""
	}

	return r.Name
}

// IsDocumentRoot reports whether this record may appear as the root of a document.
func (r *RecordType) IsDocumentRoot() bool {
	return r != nil && r.DocumentRoot
}

// Field looks up a field by exact name or short name.
func (r *RecordType) Field(name string) (*Field, bool) {
	if r == nil {
		return nil, false
	}

	for _, f := range r.Fields {
		if f.Name == name {
			return f, true
		}
	}

	short := shortName(name)
	for _, f := range r.Fields {
		if f.ShortName() == short {
			return f, true
		}
	}

	return nil, false
}

func (r *RecordType) isType() {}

// Schema is a flattened schema: named types in declaration order, plus document roots.
type Schema struct {
	names  []string
	byName map[string]Type
	roots  []*RecordType
}

// NewSchema builds a Schema from an ordered list of named types.
func NewSchema(types []Type) *Schema {
	s := &Schema{
		names:  make([]string, 0, len(types)),
		byName: make(map[string]Type, len(types)),
		roots:  make([]*RecordType, 0, len(types)),
	}

	for _, t := range types {
		name := t.TypeName()
		if name == "" {
			continue
		}

		if _, dup := s.byName[name]; !dup {
			s.names = append(s.names, name)
		}

		s.byName[name] = t
	}

	for _, name := range s.names {
		if r, ok := s.byName[name].(*RecordType); ok && r.DocumentRoot {
			s.roots = append(s.roots, r)
		}
	}

	return s
}

// Type resolves a named type. Names must be exact.
func (s *Schema) Type(name string) (Type, bool) {
	if s == nil {
		return nil, false
	}

	t, ok := s.byName[name]

	return t, ok
}

// Names returns every defined type name, in declaration order. The result is a
// fresh slice.
func (s *Schema) Names() []string {
	if s == nil {
		return make([]string, 0)
	}

	out := make([]string, 0, len(s.names))

	return append(out, s.names...)
}

// DocumentRoots returns the record types flagged documentRoot.
func (s *Schema) DocumentRoots() []*RecordType {
	if s == nil {
		return make([]*RecordType, 0)
	}

	out := make([]*RecordType, 0, len(s.roots))

	return append(out, s.roots...)
}

// shortName returns the trailing short name of an identifier IRI.
func shortName(id string) string {
	if i := strings.IndexByte(id, '#'); i >= 0 {
		if frag := id[i+1:]; frag != "" {
			return lastSegment(frag)
		}

		id = id[:i]
	}

	return lastSegment(id)
}

// lastSegment returns the text after the final "/", or all of s if it has none.
func lastSegment(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}

	return s
}
