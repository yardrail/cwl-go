package salad

import (
	"fmt"
	"strings"
)

// Validator diagnostic message templates.
const (
	msgWrongType    = "the value is %s, but %s was expected"
	msgFieldContext = "the %q field is not valid, because"
	msgTriedPrefix  = "tried "
	msgTriedSuffix  = ", but"
)

// errNoMatch is the placeholder error used during quiet probing.
var errNoMatch = &Error{
	Msg:      "the value does not match the expected type",
	Children: nil,
	Loc: SourceLine{
		File:  "",
		Start: Position{Line: 0, Column: 0, Offset: 0},
		End:   Position{Line: 0, Column: 0, Offset: 0},
	},
	Warning: false,
}

// severity says whether a diagnostic invalidates the document or is advisory.
type severity int

const (
	// severityWarning marks a diagnostic that does not by itself invalidate a document.
	severityWarning severity = iota
	// severityError marks a diagnostic that invalidates a document.
	severityError
)

// validateConfig holds the options one validation run was configured with.
type validateConfig struct {
	strict        bool
	strictForeign bool
}

// ValidateOption tunes validation. Pass options to Schema.Validate,
// Schema.ValidateAgainst or LoadedSchema.LoadAndValidate.
type ValidateOption func(*validateConfig)

// Strict promotes tolerant conditions (e.g. unrecognized fields) to errors.
func Strict(strict bool) ValidateOption {
	return func(c *validateConfig) { c.strict = strict }
}

// StrictForeign rejects properties from foreign vocabularies.
func StrictForeign(strict bool) ValidateOption {
	return func(c *validateConfig) { c.strictForeign = strict }
}

// typeNode is one (type, node) pair on the validation stack, guarding against infinite recursion.
type typeNode struct {
	typ  Type
	node Node
}

// validator carries the state of a single validation run. It is not safe for
// concurrent use; each entry point builds its own.
type validator struct {
	schema   *Schema
	subtypes map[string][]*RecordType
	idents   map[string]bool
	active   map[typeNode]bool
	cfg      validateConfig
	quiet    bool
}

// newValidator builds a validator for one run of s with opts applied.
func newValidator(s *Schema, opts []ValidateOption) *validator {
	v := &validator{
		schema:   s,
		subtypes: nil,
		idents:   nil,
		active:   make(map[typeNode]bool),
		cfg:      validateConfig{strict: false, strictForeign: false},
		quiet:    false,
	}
	for _, opt := range opts {
		opt(&v.cfg)
	}

	return v
}

// Validate checks doc against the schema's documentRoot types.
func (s *Schema) Validate(doc Node, opts ...ValidateOption) error {
	roots := s.DocumentRoots()
	if len(roots) == 0 {
		return Errorf(nodeLoc(doc), "the schema declares no documentRoot type, so it cannot validate a document")
	}

	v := newValidator(s, opts)
	v.indexIdentifiers(doc)

	return result(v.checkDocument(doc, roots))
}

// ValidateAgainst validates node against a specific named type.
func (s *Schema) ValidateAgainst(typeName string, node Node, opts ...ValidateOption) error {
	t, lookupErr := s.lookupType(typeName, node)
	if lookupErr != nil {
		return lookupErr
	}

	v := newValidator(s, opts)
	v.indexIdentifiers(node)

	return result(v.check(t, node))
}

// lookupType resolves typeName by exact match or unique short name.
func (s *Schema) lookupType(typeName string, node Node) (Type, *Error) {
	if t, ok := s.Type(typeName); ok {
		return t, nil
	}

	matches := make([]Type, 0, 1)
	names := make([]string, 0, 1)

	for _, name := range s.Names() {
		if shortName(name) != typeName {
			continue
		}

		if t, ok := s.Type(name); ok {
			matches = append(matches, t)
			names = append(names, name)
		}
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	if len(matches) == 0 {
		return nil, Errorf(nodeLoc(node), "the schema defines no type named %q", typeName)
	}

	return nil, Errorf(nodeLoc(node),
		"the type name %q is ambiguous: it could be any of %s", typeName, strings.Join(names, ", "))
}

// result returns nil unless the tree contains a non-advisory leaf.
func result(e *Error) error {
	if !isFatal(e) {
		return nil
	}

	return e
}

// isFatal reports whether the tree contains any non-warning leaf.
func isFatal(e *Error) bool {
	if e == nil {
		return false
	}

	for _, leaf := range e.Leaves() {
		if !leaf.Warning {
			return true
		}
	}

	return false
}

// checkDocument validates a whole document against the documentRoot candidates.
func (v *validator) checkDocument(doc Node, roots []*RecordType) *Error {
	alts := make([]Type, 0, len(roots))
	for _, r := range roots {
		alts = append(alts, r)
	}

	if seq, ok := AsSeq(doc); ok {
		return v.checkDocumentList(seq, alts)
	}

	if _, ok := AsMap(doc); !ok {
		return v.fail(nodeLoc(doc),
			"a document must be a mapping or a sequence of mappings, but this one is %s", describe(doc))
	}

	return v.checkAlternatives(alts, doc, rootHeader(doc))
}

// checkDocumentList validates every entry of a top-level sequence against the
// documentRoot candidates.
func (v *validator) checkDocumentList(seq *SeqNode, alts []Type) *Error {
	children := make([]*Error, 0, seq.Len())

	for i, item := range seq.All() {
		e := v.checkAlternatives(alts, item, rootHeader(item))
		if e == nil {
			continue
		}

		children = append(children, v.group(nodeLoc(item), itemContext(i), e))
	}

	return v.group(nodeLoc(seq), "", children...)
}

// rootHeader is the context line introducing the per-candidate explanations of
// why a document did not match any documentRoot type.
func rootHeader(n Node) string {
	return fmt.Sprintf("the value is %s, but it matches no documentRoot type of the schema", describe(n))
}

// check validates n against t. Single dispatch point for the recursion.
func (v *validator) check(t Type, n Node) *Error {
	if t == nil {
		return v.fail(nodeLoc(n), "the schema declares no type for this value")
	}

	key := typeNode{typ: t, node: n}
	if v.active[key] {
		return v.fail(nodeLoc(n), "the schema is self-referential: %s cannot be resolved for this value", typeLabel(t))
	}

	v.active[key] = true
	defer delete(v.active, key)

	switch tt := t.(type) {
	case *PrimitiveType:
		return v.checkPrimitive(tt, n)
	case *EnumType:
		return v.checkEnum(tt, n)
	case *ArrayType:
		return v.checkArray(tt, n)
	case *MapType:
		return v.checkMap(tt, n)
	case *UnionType:
		return v.checkUnion(tt, n)
	case *RecordType:
		return v.checkRecord(tt, n)
	default:
		// Unreachable: the switch above already names all six implementations
		// of the sealed Type interface, and t == nil is handled earlier.
		return v.fail(nodeLoc(n), "the schema uses an unsupported type %T", t)
	}
}

// probe reports whether n is valid against t, suppressing diagnostics.
func (v *validator) probe(t Type, n Node) bool {
	if v.quiet {
		return v.check(t, n) == nil
	}

	v.quiet = true
	e := v.check(t, n)
	v.quiet = false

	return !isFatal(e)
}

// fail raises a diagnostic that invalidates the document.
func (v *validator) fail(loc SourceLine, format string, a ...any) *Error {
	if v.quiet {
		return errNoMatch
	}

	return Errorf(loc, format, a...)
}

// diag raises a diagnostic at the configured severity.
func (v *validator) diag(sev severity, loc SourceLine, format string, a ...any) *Error {
	if sev == severityError {
		return v.fail(loc, format, a...)
	}

	if v.quiet {
		return nil
	}

	return Warnf(loc, format, a...)
}

// group gathers child diagnostics under a context line. Returns nil if all nil.
func (v *validator) group(loc SourceLine, msg string, children ...*Error) *Error {
	kept := make([]*Error, 0, len(children))

	for _, c := range children {
		if c != nil {
			kept = append(kept, c)
		}
	}

	if len(kept) == 0 {
		return nil
	}

	if v.quiet {
		return errNoMatch
	}

	return Group(loc, msg, kept...)
}

// strictSeverity is the severity of a diagnostic governed by the Strict option.
func (v *validator) strictSeverity() severity {
	if v.cfg.strict {
		return severityError
	}

	return severityWarning
}

// foreignSeverity is the severity of a diagnostic governed by StrictForeign.
func (v *validator) foreignSeverity() severity {
	if v.cfg.strictForeign {
		return severityError
	}

	return severityWarning
}

// wrongType reports a type mismatch.
func (v *validator) wrongType(n Node, want string) *Error {
	return v.fail(nodeLoc(n), msgWrongType, describe(n), want)
}

// nodeLoc reports where n came from, tolerating an absent node.
func nodeLoc(n Node) SourceLine {
	if n == nil {
		return SourceLine{
			File:  "",
			Start: Position{Line: 0, Column: 0, Offset: 0},
			End:   Position{Line: 0, Column: 0, Offset: 0},
		}
	}

	return n.Loc()
}

// describe returns the kind of n with an article (e.g. "a string").
func describe(n Node) string {
	kind := NodeKind(n)

	switch kind {
	case nameNull, nameNothing:
		return kind
	case nameInt:
		return "an " + kind
	default:
		return "a " + kind
	}
}

// itemContext is the context line introducing a diagnostic about one item of a
// sequence.
func itemContext(i int) string {
	return fmt.Sprintf("item %d is not valid, because", i)
}
