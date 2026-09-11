package salad

// typePair is one (sub, super) pair on the subtype-check stack, guarding against cycles.
type typePair struct {
	sub   Type
	super Type
}

// subtypeCheck carries the state of one [Schema.IsSubtype] call.
type subtypeCheck struct {
	schema *Schema
	active map[typePair]bool
}

// IsSubtype reports whether sub is a structural subtype of super.
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

// compareKinds decides a pair of concrete, non-union types.
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

// compareDeclared decides a pair of named types (enums or records).
func (c *subtypeCheck) compareDeclared(sub, super Type) bool {
	switch sup := super.(type) {
	case *EnumType:
		e, ok := sub.(*EnumType)

		return ok && c.enumNarrows(e, sup)
	case *RecordType:
		r, ok := sub.(*RecordType)

		return ok && c.recordNarrows(r, sup)
	default:
		// Unreachable: the sealed Type interface has six implementations, and
		// compare() already peels off UnionType and Any before compareKinds
		// hands *EnumType/*RecordType down to compareDeclared, which handles
		// both explicitly above; compareKinds itself handles the rest.
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

// enumNarrows reports whether sub's symbols are a subset of super's.
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

// recordNarrows reports whether sub narrows super, by extends or structurally.
func (c *subtypeCheck) recordNarrows(sub, super *RecordType) bool {
	if sameTypeName(sub.Name, super.Name) {
		return true
	}

	if c.extendsTransitively(sub, super.Name) {
		return true
	}

	return c.fieldsNarrow(sub, super)
}

// fieldsNarrow reports whether sub supplies every field super requires at a narrowing type.
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

// extendsTransitively reports whether sub transitively extends superName.
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

// narrowsAny reports whether t narrows Any (all types except null-admitting ones).
func narrowsAny(t Type) bool {
	if u, ok := t.(*UnionType); ok && len(u.Options) == 0 {
		return false
	}

	return !acceptsNull(t)
}

// sameTypeName reports whether two named types share the same non-empty name.
func sameTypeName(a, b string) bool {
	return a != "" && a == b
}
