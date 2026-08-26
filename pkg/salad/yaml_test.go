package salad

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestParseSourceLines(t *testing.T) {
	t.Parallel()

	src := "class: Tool\n" + // line 1
		"inputs:\n" + // line 2
		"  - id: first\n" + // line 3
		"    type: string\n" + // line 4
		"outputs: []\n" // line 5

	root := mustMap(t, mustParse(t, "wf.cwl", src))

	// A mapping is located at its first key, not at the ":" that follows it.
	assertLoc(t, root.Loc(), "wf.cwl", 1, 1)
	assertLoc(t, mustGet(t, root, "class").Loc(), "wf.cwl", 1, 8)

	seq := mustSeq(t, mustGet(t, root, fieldInputs))
	assertLoc(t, seq.Loc(), "wf.cwl", 3, 3)

	first := mustMap(t, seq.At(0))
	assertLoc(t, first.Loc(), "wf.cwl", 3, 5)
	assertLoc(t, mustGet(t, first, "type").Loc(), "wf.cwl", 4, 11)
}

func TestParseOffsetIsZeroBased(t *testing.T) {
	t.Parallel()

	src := "a: 1\nb: 2\n"
	b := mustGet(t, mustMap(t, mustParse(t, "t.yml", src)), "b")

	if got, want := b.Loc().Start.Offset, strings.Index(src, "2"); got != want {
		t.Errorf("Offset = %d, want %d (the index of the value in the source)", got, want)
	}
}

func TestParsePreservesKeyOrder(t *testing.T) {
	t.Parallel()

	src := "zebra: 1\napple: 2\nmango: 3\nbanana: 4\ncherry: 5\nkiwi: 6\n"
	want := []string{"zebra", "apple", "mango", "banana", "cherry", "kiwi"}

	for range 25 {
		if got := mustMap(t, mustParse(t, "t.yml", src)).Keys(); !slices.Equal(got, want) {
			t.Fatalf("Keys() = %v, want %v", got, want)
		}
	}
}

func TestParseScalarKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want any
		name string
		src  string
	}{
		{name: nameString, src: "v: hello", want: "hello"},
		{name: "quoted string", src: `v: "42"`, want: "42"},
		{name: "single-quoted string", src: "v: '42'", want: "42"},
		{name: nameInt, src: "v: 42", want: int64(42)},
		{name: "negative int", src: "v: -42", want: int64(-42)},
		{name: nameFloat, src: "v: 4.25", want: mustDecimal(t, "4.25")},
		{name: nameBoolean, src: "v: true", want: true},
		{name: "explicit null", src: "v: null", want: nil},
		{name: "implicit null", src: "v:", want: nil},
		{name: "tilde null", src: "v: ~", want: nil},
		{name: "block literal", src: "v: |\n  a\n  b\n", want: "a\nb\n"},
		{name: "folded literal", src: "v: >\n  a\n  b\n", want: "a b\n"},
		{name: "str tag forces a string", src: "v: !!str 42", want: "42"},
		{name: "unknown tag is transparent", src: "v: !custom 42", want: "42"},
		{name: "infinity", src: "v: .inf", want: math.Inf(1)},
		{
			name: "an integer past MaxInt64 stays exact", src: "v: 18446744073709551615",
			want: mustDecimal(t, "18446744073709551615"),
		},

		// goccy's numeric grammar is wider than the core schema's base-ten one.
		// A literal ParseDecimal does not recognize keeps the value goccy
		// parsed and carries no lexeme, which is what the two literal
		// converters fall back to.
		{name: "a hexadecimal integer keeps goccy's value", src: "v: 0x1f", want: int64(31)},
		{name: "an octal integer keeps goccy's value", src: "v: 0o17", want: int64(15)},
		{name: "a separated integer keeps goccy's value", src: "v: 1_0", want: int64(10)},
		{name: "a separated float keeps goccy's value", src: "v: 1_0.5", want: 10.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := parseValue(t, tc.src); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("value = %#v (%T), want %#v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

// TestParseRecoversCoreSchemaNumbers pins the literals goccy's scanner declines
// to type. It resolves a plain scalar to a number only when it is an
// exponent-free decimal fitting an int64 or a uint64, or a float written with a
// decimal point; the YAML 1.2 core schema calls the rest numbers too, and a
// conformance job order that writes one as an exponent or as a magnitude past
// int64 must still reach a consumer as a number rather than as text.
func TestParseRecoversCoreSchemaNumbers(t *testing.T) {
	t.Parallel()

	const (
		// exponent is the shape goccy declines to type, in the spelling the
		// quoting cases have to repeat.
		exponent = "1e40"

		// spaced is an integer written with the YAML 1.1 digit separators the
		// 1.2 core schema dropped.
		spaced = "1_000_000_000_000_000_000_000"
	)

	tests := []struct {
		want any
		name string
		src  string
	}{
		// The literal from the conformance suite's paramref_arguments_inputs
		// job order: 1 followed by 42 zeros. It keeps every digit; widening
		// it to a float was the old answer and float(1e42) is not 10**42.
		{
			name: "a huge integer keeps its digits", src: "v: 1" + strings.Repeat("0", 42),
			want: mustDecimal(t, "1"+strings.Repeat("0", 42)),
		},
		{name: "exponent without a point", src: "v: 1e40", want: mustDecimal(t, "1e40")},
		{name: "negative exponent literal", src: "v: -1e40", want: mustDecimal(t, "-1e40")},
		{name: "capital exponent", src: "v: 1E40", want: mustDecimal(t, "1E40")},
		{name: "signed exponent", src: "v: 1e+40", want: mustDecimal(t, "1e+40")},
		{name: "point and exponent", src: "v: 1.5e300", want: mustDecimal(t, "1.5e300")},
		{name: "just past MaxInt64", src: "v: 9223372036854775808", want: mustDecimal(t, "9223372036854775808")},
		{name: "past MaxUint64", src: "v: 18446744073709551616", want: mustDecimal(t, "18446744073709551616")},
		{
			name: "negative past MinInt64", src: "v: -9223372036854775809",
			want: mustDecimal(t, "-9223372036854775809"),
		},
		{name: "an exponent past float64 stays exact", src: "v: 1e400", want: mustDecimal(t, "1e400")},

		// The one literal the grammar accepts that ParseDecimal will not
		// hold: rendering it would expand thirteen source characters into a
		// gigabyte of digits, so it falls back to the float it always was.
		{name: "an absurd exponent saturates", src: "v: 1e999999999", want: math.Inf(1)},

		// Quoting is how a document says a value is a string, and a plain
		// scalar that only resembles a number is still a string.
		{name: "double-quoted exponent stays a string", src: `v: "` + exponent + `"`, want: exponent},
		{name: "single-quoted exponent stays a string", src: "v: '" + exponent + "'", want: exponent},
		{name: "str tag beats the exponent", src: "v: !!str " + exponent, want: exponent},
		{name: "trailing text is not a number", src: "v: 1e40x", want: "1e40x"},
		{name: "two points are not a number", src: "v: 1.2.3", want: "1.2.3"},
		{name: "a bare exponent is not a number", src: "v: 1e", want: "1e"},
		{name: "NaN spelled as a word is a string", src: "v: NaN", want: "NaN"},
		{name: "Infinity spelled as a word is a string", src: "v: Infinity", want: "Infinity"},
		{name: "underscores are not core schema", src: "v: " + spaced, want: spaced},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := parseValue(t, tc.src); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("value = %#v (%T), want %#v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

// TestCoreSchemaNumberGuards exercises the recovery directly, for the two
// answers no document can currently drive it to.
//
// goccy types every plain integer that fits an int64 or a uint64 itself, so the
// in-range branch is never reached through Parse; it is kept anyway, because
// silently widening such a literal into a float would turn an int into a double
// the day goccy's scanner changes. The rejection branch is the same bet in the
// other direction: the grammar has already accepted the text, so only a
// magnitude outside float64's range can fail, and that one is admitted.
func TestCoreSchemaNumberGuards(t *testing.T) {
	t.Parallel()

	loc := SourceLine{File: "t.yml"}

	got, ok := coreSchemaNumber(loc, "42")
	if !ok {
		t.Fatal("coreSchemaNumber rejected an in-range integer")
	}

	if value := got.Value(); value != int64(42) {
		t.Errorf("value = %#v (%T), want int64(42)", value, value)
	}

	if _, ok = coreSchemaNumber(loc, "not a number"); ok {
		t.Error("coreSchemaNumber accepted text that is not a number")
	}

	if _, ok = floatNode(loc, "not a number"); ok {
		t.Error("floatNode accepted text the grammar would never have passed it")
	}
}

func TestParseNaN(t *testing.T) {
	t.Parallel()

	got, ok := parseValue(t, "v: .nan").(float64)
	if !ok || !math.IsNaN(got) {
		t.Errorf("value = %#v, want NaN", got)
	}
}

func TestParseJSONIsAcceptedByTheYAMLPath(t *testing.T) {
	t.Parallel()

	// YAML 1.2 is a superset of JSON, so a single parse path serves both. The
	// positions reported for JSON flow collections are accurate, which is what
	// makes reusing the YAML path acceptable rather than merely convenient.
	src := `{"class": "Tool", "inputs": [{"id": "a"}]}`
	m := mustMap(t, mustParse(t, "doc.json", src))

	if got, want := m.Keys(), []string{"class", fieldInputs}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}

	entry := mustMap(t, mustSeq(t, mustGet(t, m, fieldInputs)).At(0))
	assertLoc(t, mustGet(t, entry, "id").Loc(), "doc.json", 1, strings.Index(src, `"a"`)+1)
}

func checkDuplicateKeyRejected(t *testing.T, src string) {
	t.Helper()

	_, err := Parse("dup.yml", []byte(src))
	if err == nil {
		t.Fatal("Parse accepted a document with a duplicate key")
	}

	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want *Error", err)
	}

	if se.Loc.File != "dup.yml" || se.Loc.Start.Line == 0 {
		t.Errorf("error location = %v, want a line in dup.yml", se.Loc)
	}

	if !strings.Contains(se.Msg, "already defined") {
		t.Errorf("error message = %q, want it to mention the duplicate", se.Msg)
	}
}

func TestParseRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()

	// Regression guard for a known goccy caveat: duplicate-key detection has
	// historically missed inlined (flow-style) maps. Assert it fires for block,
	// flow and JSON syntax alike.
	tests := []struct {
		name string
		src  string
	}{
		{name: "block", src: "a: 1\na: 2\n"},
		{name: "flow", src: "{a: 1, a: 2}"},
		{name: "json", src: `{"a": 1, "a": 2}`},
		{name: "nested flow", src: "outer:\n  {a: 1, a: 2}\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkDuplicateKeyRejected(t, tc.src)
		})
	}
}

// mergeCase is one expected outcome of expanding a YAML merge key.
type mergeCase struct {
	want     map[string]any
	name     string
	src      string
	wantKeys []string
}

func checkMerge(t *testing.T, tc mergeCase) {
	t.Helper()

	root := mustMap(t, mustParse(t, "merge.yml", tc.src))
	derived := mustMap(t, mustGet(t, root, "derived"))

	if got := derived.Keys(); !slices.Equal(got, tc.wantKeys) {
		t.Errorf("Keys() = %v, want %v", got, tc.wantKeys)
	}

	if got := ToAny(derived); !reflect.DeepEqual(got, any(tc.want)) {
		t.Errorf("value = %#v, want %#v", got, tc.want)
	}
}

func TestParseMergeKeys(t *testing.T) {
	t.Parallel()

	tests := []mergeCase{
		{
			name: "merge supplies defaults",
			src: "base: &b\n  x: 1\n  y: 2\n" +
				"derived:\n  <<: *b\n  y: 3\n",
			wantKeys: []string{"x", "y"},
			want:     map[string]any{"x": int64(1), "y": int64(3)},
		},
		{
			name: "own key wins even when it comes after the merge",
			src: "base: &b\n  x: 1\n" +
				"derived:\n  <<: *b\n  x: 99\n",
			wantKeys: []string{"x"},
			want:     map[string]any{"x": int64(99)},
		},
		{
			name: "earlier merge source wins",
			src: "one: &a\n  k: 1\ntwo: &b\n  k: 2\n  extra: 3\n" +
				"derived:\n  <<: [*a, *b]\n",
			wantKeys: []string{"k", "extra"},
			want:     map[string]any{"k": int64(1), "extra": int64(3)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkMerge(t, tc)
		})
	}
}

func TestParseMergeKeyRejectsNonMapping(t *testing.T) {
	t.Parallel()

	_, err := Parse("merge.yml", []byte("a: &x 1\nb:\n  <<: *x\n"))
	if err == nil {
		t.Fatal("Parse accepted a merge key whose value is not a mapping")
	}

	if !strings.Contains(err.Error(), "requires a mapping") {
		t.Errorf("error = %q, want it to explain the merge key requirement", err)
	}
}

func TestParseAliases(t *testing.T) {
	t.Parallel()

	n := mustParse(t, testFile, "first: &shared\n  k: v\nsecond: *shared\n")
	want := map[string]any{
		"first":  map[string]any{"k": "v"},
		"second": map[string]any{"k": "v"},
	}

	if got := ToAny(n); !reflect.DeepEqual(got, any(want)) {
		t.Errorf("value = %#v, want %#v", got, want)
	}
}

func TestParseUndefinedAlias(t *testing.T) {
	t.Parallel()

	_, err := Parse(testFile, []byte("a: *missing\n"))
	if err == nil {
		t.Fatal("Parse accepted a reference to an undefined anchor")
	}

	if !strings.Contains(err.Error(), "undefined YAML anchor") {
		t.Errorf("error = %q, want it to name the undefined anchor", err)
	}
}

func TestParseNonStringKeys(t *testing.T) {
	t.Parallel()

	m := mustMap(t, mustParse(t, "k.yml", "1: one\ntrue: yes\nnull: nothing\n"))
	if got, want := m.Keys(), []string{"1", "true", nameNull}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
}

func TestParseEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want any
		name string
		src  string
	}{
		{name: "empty document", src: "", want: nil},
		{name: "comments only", src: "# nothing here\n", want: nil},
		{name: "scalar root", src: "hello\n", want: "hello"},
		{name: "empty mapping", src: "{}", want: make(map[string]any)},
		{name: "empty sequence", src: "[]", want: make([]any, 0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ToAny(mustParse(t, "e.yml", tc.src)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("value = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestParseRejectsMultipleDocuments(t *testing.T) {
	t.Parallel()

	_, err := Parse("multi.yml", []byte("a: 1\n---\nb: 2\n"))
	if err == nil {
		t.Fatal("Parse accepted a multi-document stream")
	}

	if !strings.Contains(err.Error(), "single YAML document") {
		t.Errorf("error = %q, want it to explain the single-document rule", err)
	}
}

func TestParseSyntaxErrorCarriesPosition(t *testing.T) {
	t.Parallel()

	_, err := Parse("bad.yml", []byte("a: [1, 2\n"))
	if err == nil {
		t.Fatal("Parse accepted malformed YAML")
	}

	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want *Error", err)
	}

	if se.Loc.File != "bad.yml" || se.Loc.Start.Line != 1 {
		t.Errorf("error location = %v, want bad.yml line 1", se.Loc)
	}

	if strings.Contains(se.Error(), "\n") {
		t.Errorf("Error() spans several lines: %q", se.Error())
	}
}

// parseValue parses a one-key document and returns that key's plain value.
func parseValue(t *testing.T, src string) any {
	t.Helper()

	return ToAny(mustGet(t, mustMap(t, mustParse(t, "t.yml", src)), "v"))
}
