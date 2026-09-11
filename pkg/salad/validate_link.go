package salad

import "strings"

// jsonldID is the JSON-LD keyword for identifier/link fields.
const jsonldID = "@id"

// indexIdentifiers collects all identifiers before validation for link checking.
func (v *validator) indexIdentifiers(doc Node) {
	v.idents = make(map[string]bool)

	keys := v.identifierKeys()
	if len(keys) == 0 {
		return
	}

	collectIdentifiers(doc, keys, v.idents)
}

// identifierKeys returns the set of identifier field keys (full and short).
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

// collectIdentifiers walks a document, recording values of identifier-keyed entries.
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

// checkLink validates that link fields refer to declared identifiers.
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

// isLinkField reports whether a field holds a link (not an identifier or exempt).
func isLinkField(f *Field) bool {
	pred := f.JSONLDPred
	if pred == nil || pred.Type != jsonldID {
		return false
	}

	return pred.ID != jsonldID && !pred.Identity && !pred.NoLinkCheck
}

// hasURIScheme reports whether a reference has a URI scheme (is absolute).
func hasURIScheme(ref string) bool {
	i := strings.IndexByte(ref, ':')
	if i <= 0 {
		return false
	}

	return isSchemeName(ref[:i])
}

// isSchemeName reports whether s is a valid URI scheme name.
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

// isSchemeSymbol reports whether r is valid after a URI scheme's first character.
func isSchemeSymbol(r rune) bool {
	return (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.'
}
