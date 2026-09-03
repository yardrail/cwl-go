package salad

import (
	"fmt"
	"strings"
)

// maxLabelDepth bounds how far typeLabel descends into an anonymous type before
// eliding the rest. A schema type graph may be self-referential, so rendering it
// has to stop somewhere.
const maxLabelDepth = 3

// labelEllipsis replaces the part of a type label that maxLabelDepth cut off.
const labelEllipsis = "..."

// checkAlternatives validates n against a set of candidate types, and is where
// the union strategy inherited from schema-salad lives: every candidate is first
// probed with diagnostics suppressed, and only once all of them have failed is
// each one re-run verbosely so the reader gets one child error per candidate
// saying why that candidate did not match.
//
// header introduces those children; it is phrased by the caller because the
// candidates mean different things — union members, documentRoot types, or the
// concrete subtypes of an abstract record.
func (v *validator) checkAlternatives(alts []Type, n Node, header string) *Error {
	if len(alts) == 0 {
		return v.fail(nodeLoc(n), "the schema offers no type for this value")
	}

	if len(alts) == 1 {
		return v.check(alts[0], n)
	}

	for _, alt := range alts {
		if v.probe(alt, n) {
			return nil
		}
	}

	if v.quiet {
		return errNoMatch
	}

	return v.explainAlternatives(alts, n, header)
}

// explainAlternatives re-runs every candidate verbosely, after they have all
// been probed and rejected, to collect the per-candidate explanation.
//
// Both "e == nil" below and the "len(children) == 0" fallback that follows
// exist for a rerun that disagrees with the probe that rejected the same
// candidate. No fixture has been found that drives that disagreement: check
// is a function of (v.active-at-entry, t, n, v.quiet), fail and diag build
// the same non-nil result under quiet and verbose alike (diag's own quiet
// branch only ever drops a warning, which isFatal already treats as
// non-fatal in the verbose result too), and v.active is restored by defer
// before each alt in a loop is either probed or rerun, so the two passes see
// the same entry state pair by pair. These are kept as the defensive
// completion of "a probe said no, but the verbose recheck said yes" rather
// than chased with a contrived fixture.
func (v *validator) explainAlternatives(alts []Type, n Node, header string) *Error {
	children := make([]*Error, 0, len(alts))

	for _, alt := range alts {
		e := v.check(alt, n)
		if e == nil {
			continue
		}

		children = append(
			children,
			Group(
				SourceLine{
					File:  "",
					Start: Position{Line: 0, Column: 0, Offset: 0},
					End:   Position{Line: 0, Column: 0, Offset: 0},
				},
				msgTriedPrefix+typeLabel(alt)+msgTriedSuffix,
				e,
			),
		)
	}

	if len(children) == 0 {
		return v.fail(nodeLoc(n), "%s", header)
	}

	return Group(nodeLoc(n), header, children...)
}

// checkUnion validates n against a union of alternative types.
//
// Two shapes of union get a plainer message than the general per-candidate tree.
// A null value in a union that does not accept null is reported as exactly that,
// rather than as one rejection per member; and a non-null value is never tried
// against the null member, because "tried null, but the value is a string" tells
// a reader nothing. Dropping null also collapses the overwhelmingly common
// optional field, ["null", T], to a direct diagnostic about T.
func (v *validator) checkUnion(u *UnionType, n Node) *Error {
	if IsNull(n) {
		if u.HasNull() {
			return nil
		}

		return v.fail(nodeLoc(n), "the value is null, but %s was expected", typeLabel(u))
	}

	alts := withoutNull(u.Options)
	if len(alts) == 0 {
		return v.wrongType(n, typeLabel(u))
	}

	return v.checkAlternatives(alts, n, unionHeader(u, n))
}

// unionHeader introduces the per-member explanations of a rejected union value.
func unionHeader(u *UnionType, n Node) string {
	return fmt.Sprintf("the value is %s, but %s was expected", describe(n), typeLabel(u))
}

// withoutNull returns the options with the null primitive removed.
func withoutNull(opts []Type) []Type {
	out := make([]Type, 0, len(opts))

	for _, opt := range opts {
		if p, ok := opt.(*PrimitiveType); ok && p.Kind == PrimitiveNull {
			continue
		}

		out = append(out, opt)
	}

	return out
}

// checkAbstract validates n against the concrete subtypes of an abstract record.
//
// The specification says an abstract type "is not used for validation on its
// own, but may be extended by other definitions", and that where one appears in
// a field definition "it is logically replaced with a union of all concrete
// subtypes of the abstract type" — so this is exactly a union check over the
// subtypes the schema declares.
func (v *validator) checkAbstract(r *RecordType, n Node) *Error {
	subs := v.concreteSubtypes(r)
	if len(subs) == 0 {
		return v.fail(nodeLoc(n), "%s is abstract and the schema declares no concrete subtype of it", typeLabel(r))
	}

	header := fmt.Sprintf("the value is %s, but no concrete subtype of %s matches", describe(n), typeLabel(r))

	return v.checkAlternatives(subs, n, header)
}

// concreteSubtypes returns every concrete record that extends r, directly or
// transitively, in schema declaration order.
func (v *validator) concreteSubtypes(r *RecordType) []Type {
	return v.appendConcrete(make([]Type, 0), make(map[string]bool), r)
}

// appendConcrete walks the subtype tree below r, appending concrete records and
// descending through abstract ones. The seen set keeps a diamond in the
// inheritance graph from yielding a type twice, and a cycle from looping.
func (v *validator) appendConcrete(dst []Type, seen map[string]bool, r *RecordType) []Type {
	for _, child := range v.childrenOf(r) {
		if seen[child.Name] {
			continue
		}

		seen[child.Name] = true

		if child.Abstract {
			dst = v.appendConcrete(dst, seen, child)

			continue
		}

		dst = append(dst, child)
	}

	return dst
}

// childrenOf returns the records that directly extend r.
func (v *validator) childrenOf(r *RecordType) []*RecordType {
	v.ensureSubtypeIndex()

	direct := v.subtypes[r.Name]

	short := shortName(r.Name)
	if short == r.Name {
		return direct
	}

	out := make([]*RecordType, 0, len(direct)+len(v.subtypes[short]))
	out = append(out, direct...)

	return append(out, v.subtypes[short]...)
}

// ensureSubtypeIndex builds, once per run, the base-name to direct-subtype index
// that abstract expansion walks.
//
// An extends entry is indexed under both the name as written and its short name,
// because a schema may spell a base type either way and Schema.Type resolves
// only exact names.
func (v *validator) ensureSubtypeIndex() {
	if v.subtypes != nil {
		return
	}

	v.subtypes = make(map[string][]*RecordType)

	for _, name := range v.schema.Names() {
		// Unreachable: name comes from v.schema.Names() itself, and
		// NewSchema's byName/names invariant guarantees Type(name) then
		// always succeeds.
		t, ok := v.schema.Type(name)
		if !ok {
			continue
		}

		r, ok := t.(*RecordType)
		if !ok {
			continue
		}

		v.indexExtends(r)
	}
}

// indexExtends records r as a subtype of each of its declared base types.
func (v *validator) indexExtends(r *RecordType) {
	for _, base := range r.Extends {
		v.subtypes[base] = append(v.subtypes[base], r)

		if short := shortName(base); short != base {
			v.subtypes[short] = append(v.subtypes[short], r)
		}
	}
}

// typeLabel renders a type the way an error message should name it: a named type
// by its short name, and an anonymous one by its structure.
func typeLabel(t Type) string {
	return typeLabelAt(t, 0)
}

// typeLabelAt renders t's label, eliding any structure below maxLabelDepth so
// that a self-referential schema type graph still renders.
func typeLabelAt(t Type, depth int) string {
	if t == nil {
		return nameNothing
	}

	if name := t.TypeName(); name != "" {
		return shortName(name)
	}

	if depth >= maxLabelDepth {
		return labelEllipsis
	}

	switch tt := t.(type) {
	case *ArrayType:
		return "an array of " + typeLabelAt(tt.Items, depth+1)
	case *MapType:
		return "a map of " + typeLabelAt(tt.Values, depth+1)
	case *UnionType:
		return unionLabel(tt, depth)
	default:
		return nameUnknown
	}
}

// unionLabel renders "one of a, b, c", collapsing to the single alternative when
// null is the only other member — which is how an optional value is spelled.
func unionLabel(u *UnionType, depth int) string {
	opts := withoutNull(u.Options)
	if len(opts) == 0 {
		return nameNull
	}

	if len(opts) == 1 {
		return typeLabelAt(opts[0], depth+1)
	}

	parts := make([]string, 0, len(opts))
	for _, opt := range opts {
		parts = append(parts, typeLabelAt(opt, depth+1))
	}

	return "one of " + strings.Join(parts, ", ")
}
