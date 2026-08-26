package salad

import "strings"

// jsonldID is the JSON-LD keyword a jsonldPredicate uses to say that a field
// holds an identifier (as its _id) or a link to one (as its _type).
const jsonldID = "@id"

// indexIdentifiers collects, before validation begins, every identifier the
// document declares, so that a link can be checked whether or not its target
// appears earlier in the document than the reference to it.
//
// The walk is driven by key name rather than by resolving each subtree's type:
// which keys are identifiers is a property of the schema, and collecting a value
// that happens to sit under the same key somewhere the schema does not declare
// an identifier only ever makes link checking more permissive, never less.
func (v *validator) indexIdentifiers(doc Node) {
	v.idents = make(map[string]bool)

	keys := v.identifierKeys()
	if len(keys) == 0 {
		return
	}

	collectIdentifiers(doc, keys, v.idents)
}

// identifierKeys returns the set of keys that name an identifier field anywhere
// in the schema, under both their full and short spellings.
func (v *validator) identifierKeys() map[string]bool {
	keys := make(map[string]bool)

	for _, name := range v.schema.Names() {
		r, ok := mustRecord(v.schema, name)
		if !ok {
			continue
		}

		for _, f := range r.Fields {
			if f.JSONLDPred != nil && f.JSONLDPred.ID == jsonldID {
				keys[f.Name] = true
				keys[f.ShortName()] = true
			}
		}
	}

	return keys
}

// mustRecord resolves a named type and reports whether it is a record.
func mustRecord(s *Schema, name string) (*RecordType, bool) {
	t, ok := s.Type(name)
	if !ok {
		return nil, false
	}

	r, ok := t.(*RecordType)

	return r, ok
}

// collectIdentifiers walks a document tree, recording the string value of every
// entry whose key names an identifier field.
func collectIdentifiers(n Node, keys, out map[string]bool) {
	switch node := n.(type) {
	case *MapNode:
		for key, value := range node.All() {
			if id, ok := AsString(value); ok && keys[key] {
				out[id] = true
			}

			collectIdentifiers(value, keys, out)
		}
	case *SeqNode:
		for _, item := range node.Items() {
			collectIdentifiers(item, keys, out)
		}
	default:
	}
}

// checkLink validates that a field declared to hold a link refers to an
// identifier the document declares.
//
// Only references this package can settle on its own are checked. A value
// carrying a URI scheme names something in another document, which only the
// loader — which fetches those documents and holds the identifier index across
// them — is in a position to resolve, so it is left alone. A field whose
// jsonldPredicate sets identity is likewise left alone: the specification says
// for such a field that "absence of an object in the loaded document with the
// URI is not an error".
//
// Because a reference is often legitimately resolvable only with the loader's
// wider view, an unresolved link is advisory by default and is promoted to an
// error by Strict.
func (v *validator) checkLink(f *Field, value Node) *Error {
	if !isLinkField(f) {
		return nil
	}

	switch node := value.(type) {
	case *ScalarNode:
		return v.checkLinkTarget(node)
	case *SeqNode:
		return v.checkLinkTargets(node)
	default:
		return nil
	}
}

// checkLinkTargets validates every link in a list-valued link field.
func (v *validator) checkLinkTargets(seq *SeqNode) *Error {
	children := make([]*Error, 0, seq.Len())

	for _, item := range seq.Items() {
		if s, ok := AsScalar(item); ok {
			children = append(children, v.checkLinkTarget(s))
		}
	}

	return v.group(seq.Loc(), "", children...)
}

// checkLinkTarget validates one link value.
func (v *validator) checkLinkTarget(s *ScalarNode) *Error {
	target, ok := s.AsString()
	if !ok || target == "" {
		return nil
	}

	if hasURIScheme(target) || v.idents[target] {
		return nil
	}

	return v.diag(v.strictSeverity(), s.Loc(),
		"the link %q refers to no identifier declared in this document", target)
}

// isLinkField reports whether a field holds a link to an identifier, rather than
// declaring one or being exempt from link checking.
//
// Three jsonldPredicate settings exempt a field. A field whose _id is "@id"
// declares an identifier instead of referring to one. identity says that
// "absence of an object in the loaded document with the URI is not an error".
// noLinkCheck says that validation traversal must stop at the field, so nothing
// below it is a link at all.
func isLinkField(f *Field) bool {
	pred := f.JSONLDPred
	if pred == nil || pred.Type != jsonldID {
		return false
	}

	return pred.ID != jsonldID && !pred.Identity && !pred.NoLinkCheck
}

// hasURIScheme reports whether a reference is absolute, which is to say resolved
// against some document other than the one being validated.
func hasURIScheme(ref string) bool {
	i := strings.IndexByte(ref, ':')
	if i <= 0 {
		return false
	}

	return isSchemeName(ref[:i])
}

// isSchemeName reports whether s is shaped like a URI scheme: a letter followed
// by letters, digits, "+", "-" or ".".
func isSchemeName(s string) bool {
	if !isSchemeLetter(rune(s[0])) {
		return false
	}

	for _, r := range s[1:] {
		if !isSchemeLetter(r) && !isSchemeSymbol(r) {
			return false
		}
	}

	return true
}

// isSchemeLetter reports whether r may start a URI scheme.
func isSchemeLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isSchemeSymbol reports whether r may appear after the first character of a URI
// scheme.
func isSchemeSymbol(r rune) bool {
	return (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.'
}
