package salad

import "testing"

// Fixture names shared across the package's tests, collected so that no literal
// is repeated often enough to be worth a constant of its own.
const (
	testFile    = "a.yml"
	typeTool    = "Tool"
	nameProcess = "Process"
	fieldClass  = "class"
	fieldInputs = "inputs"
	litGreen    = "green"
)

// entries builds MapNode contents from alternating key/value strings.
func entries(pairs ...string) []MapEntry {
	out := make([]MapEntry, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(
			out,
			MapEntry{
				Key: pairs[i],
				Value: NewStringNode(
					SourceLine{
						File:  "",
						Start: Position{Line: 0, Column: 0, Offset: 0},
						End:   Position{Line: 0, Column: 0, Offset: 0},
					},
					pairs[i+1],
				),
			},
		)
	}

	return out
}

// mustGet fetches a key that the test requires to be present.
func mustGet(t *testing.T, m *MapNode, key string) Node {
	t.Helper()

	n, ok := m.Get(key)
	if !ok {
		t.Fatalf("key %q is missing", key)
	}

	return n
}

// mustMap asserts that n is a mapping and returns it.
func mustMap(t *testing.T, n Node) *MapNode {
	t.Helper()

	m, ok := AsMap(n)
	if !ok {
		t.Fatalf("node is %s, want a mapping", NodeKind(n))
	}

	return m
}

// mustSeq asserts that n is a sequence and returns it.
func mustSeq(t *testing.T, n Node) *SeqNode {
	t.Helper()

	s, ok := AsSeq(n)
	if !ok {
		t.Fatalf("node is %s, want a sequence", NodeKind(n))
	}

	return s
}

// mustParse parses src, failing the test if it does not parse.
func mustParse(t *testing.T, file, src string) Node {
	t.Helper()

	n, err := Parse(file, []byte(src))
	if err != nil {
		t.Fatalf("Parse(%q) failed: %v", src, err)
	}

	return n
}

// fieldArrayElem resolves the element type of a record field that must be an
// array.
func fieldArrayElem(t *testing.T, r *RecordType, name string) Type {
	t.Helper()

	f, found := r.Field(name)
	if !found {
		t.Fatalf("record has no %s field", name)
	}

	if elem := arrayElementOf(f.Type); elem != nil {
		return elem
	}

	if a, ok := f.Type.(*ArrayType); ok {
		return a.Items
	}

	t.Fatalf("%s field is not an array; type = %s", name, typeLabel(f.Type))

	return nil
}

// assertLoc checks the file, line and column a node reports.
func assertLoc(t *testing.T, got SourceLine, file string, line, col int) {
	t.Helper()

	if got.File != file || got.Start.Line != line || got.Start.Column != col {
		t.Errorf("location = %s, want %s:%d:%d", got, file, line, col)
	}
}
