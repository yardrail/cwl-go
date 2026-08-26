package salad

// typePair is one (sub, super) pair currently on the subtype-check stack. The
// flattened type graph is cyclic — a record's field may lead back to the record
// itself — so the walk has to remember which pairs it is already deciding.
type typePair struct {
	sub   Type
	super Type
}

// subtypeCheck carries the state of one IsSubtype call. It is not safe for
// concurrent use; each call builds its own.
type subtypeCheck struct {
	schema *Schema
	active map[typePair]bool
}

// IsSubtype reports whether sub is a structural subtype of super, resolving named
// references through the schema's name table.
//
// It descends arrays, maps, unions, enums and records, and is the check that
// validates that a field re-specified by an extending record narrows the
// inherited type rather than widening it.
//
// A pair already being decided further up the walk is assumed to hold, which is
// what lets a self-referential schema be compared at all: the answer is the
// greatest fixed point, the same convention recursive structural subtyping uses
// elsewhere.
//
// It is the analogue of avro.schema.is_subtype.
func (s *Schema) IsSubtype(sub, super Type) bool {
	c := &subtypeCheck{schema: s, active: make(map[typePair]bool)}

	return c.check(sub, super)
}

// check decides one pair, maintaining the guard against re-entering it.
func (c *subtypeCheck) check(sub, super Type) bool {
	if sub == nil || super == nil {
		return sub == nil && super == nil
	}

	if sub == super {
		return true
	}

	key := typePair{sub: sub, super: super}
	if c.active[key] {
		return true
	}

	c.active[key] = true
	defer delete(c.active, key)

	return c.compare(sub, super)
}

// compare decides a pair that is neither nil nor already on the stack.
//
// Any and the unions are handled before the per-kind comparison because they are
// not structural: Any admits every type that cannot be null, a union on the sub
// side must have every alternative admitted, and a union on the super side needs
// only one alternative to admit the value.
func (c *subtypeCheck) compare(sub, super Type) bool {
	if isAnyType(super) {
		return narrowsAny(sub)
	}

	if u, ok := sub.(*UnionType); ok {
		return c.everyOption(u.Options, super)
	}

	if u, ok := super.(*UnionType); ok {
		return c.someOption(sub, u.Options)
	}

	return c.compareKinds(sub, super)
}

// compareKinds decides a pair of concrete, non-union types whose shape is what
// settles the question.
func (c *subtypeCheck) compareKinds(sub, super Type) bool {
	switch sup := super.(type) {
	case *PrimitiveType:
		p, ok := sub.(*PrimitiveType)

		return ok && p.Kind == sup.Kind
	case *ArrayType:
		a, ok := sub.(*ArrayType)

		return ok && c.check(a.Items, sup.Items)
	case *MapType:
		m, ok := sub.(*MapType)

		return ok && c.check(m.Values, sup.Values)
	default:
		return c.compareDeclared(sub, super)
	}
}

// compareDeclared decides a pair of types the schema declares by name, where the
// declaration itself carries the answer: an enum's symbols, or a record's bases
// and fields.
func (c *subtypeCheck) compareDeclared(sub, super Type) bool {
	switch sup := super.(type) {
	case *EnumType:
		e, ok := sub.(*EnumType)

		return ok && c.enumNarrows(e, sup)
	case *RecordType:
		r, ok := sub.(*RecordType)

		return ok && c.recordNarrows(r, sup)
	default:
		return false
	}
}

// everyOption reports whether every alternative of a union on the sub side is
// admitted by super.
func (c *subtypeCheck) everyOption(opts []Type, super Type) bool {
	for _, opt := range opts {
		if !c.check(opt, super) {
			return false
		}
	}

	return true
}

// someOption reports whether any alternative of a union on the super side admits
// sub.
func (c *subtypeCheck) someOption(sub Type, opts []Type) bool {
	for _, opt := range opts {
		if c.check(sub, opt) {
			return true
		}
	}

	return false
}

// enumNarrows reports whether one enum's symbols are a subset of another's. The
// same enum by name is trivially a subtype of itself.
func (c *subtypeCheck) enumNarrows(sub, super *EnumType) bool {
	if sameTypeName(sub.Name, super.Name) {
		return true
	}

	for _, sym := range sub.Symbols {
		if !super.HasSymbol(sym) {
			return false
		}
	}

	return true
}

// recordNarrows reports whether one record narrows another.
//
// A record narrows a record it declares, directly or transitively, as a base:
// that is what extends means, and it is the case the flattener's narrowing check
// exists to accept. Failing that the comparison is structural.
func (c *subtypeCheck) recordNarrows(sub, super *RecordType) bool {
	if sameTypeName(sub.Name, super.Name) {
		return true
	}

	if c.extendsTransitively(sub, super.Name) {
		return true
	}

	return c.fieldsNarrow(sub, super)
}

// fieldsNarrow reports whether sub supplies every field super requires, each at a
// narrowing type.
//
// A field super declares but sub does not is fatal unless super lets it be null,
// since a value of the narrower record would then be missing something the wider
// record requires. schema-salad checks this the other way round — every field of
// the narrower record must appear in the wider one — which makes a record a
// subtype of anything that merely declares a superset of its fields. The
// specification says an extending record may re-specify inherited fields "to
// narrow their type", so narrowing is what is checked here.
func (c *subtypeCheck) fieldsNarrow(sub, super *RecordType) bool {
	for _, sf := range super.Fields {
		f, ok := sub.Field(sf.Name)
		if !ok {
			if acceptsNull(sf.Type) {
				continue
			}

			return false
		}

		if !c.check(f.Type, sf.Type) {
			return false
		}
	}

	return true
}

// extendsTransitively reports whether sub reaches superName by following extends
// declarations through the schema's name table.
func (c *subtypeCheck) extendsTransitively(sub *RecordType, superName string) bool {
	if superName == "" {
		return false
	}

	seen := make(map[string]bool, len(sub.Extends))
	queue := append(make([]string, 0, len(sub.Extends)), sub.Extends...)

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		if seen[name] {
			continue
		}

		seen[name] = true

		if name == superName {
			return true
		}

		if base, ok := c.recordNamed(name); ok {
			queue = append(queue, base.Extends...)
		}
	}

	return false
}

// recordNamed resolves a base type name to the record the schema defines for it.
func (c *subtypeCheck) recordNamed(name string) (*RecordType, bool) {
	t, ok := c.schema.Type(name)
	if !ok {
		return nil, false
	}

	r, ok := t.(*RecordType)

	return r, ok
}

// isAnyType reports whether t is the Any primitive, which admits any non-null
// value.
func isAnyType(t Type) bool {
	p, ok := t.(*PrimitiveType)

	return ok && p.Kind == PrimitiveAny
}

// narrowsAny reports whether t narrows Any, which every type does except those
// that admit null and the empty union, which admits nothing at all.
func narrowsAny(t Type) bool {
	if u, ok := t.(*UnionType); ok && len(u.Options) == 0 {
		return false
	}

	return !acceptsNull(t)
}

// sameTypeName reports whether two named types carry the same name. Anonymous
// types share the empty name without being the same type, so they never match.
func sameTypeName(a, b string) bool {
	return a != "" && a == b
}
