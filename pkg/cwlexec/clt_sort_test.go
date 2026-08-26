package cwlexec

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestCompareElems(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    keyElem
		b    keyElem
		want int
	}{
		{name: "equal numbers", a: numKey(3), b: numKey(3), want: 0},
		{name: "smaller number first", a: numKey(-1), b: numKey(0), want: -1},
		{name: "larger number last", a: numKey(10), b: numKey(2), want: 1},
		{name: "equal strings", a: textKey("a"), b: textKey("a"), want: 0},
		{name: "strings sort lexicographically", a: textKey("alpha"), b: textKey("beta"), want: -1},
		{name: "strings compare by code unit", a: textKey("Z"), b: textKey("a"), want: -1},
		{name: "a number sorts before a string", a: numKey(99), b: textKey("0"), want: -1},
		{name: "a string sorts after a number", a: textKey("0"), b: numKey(99), want: 1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := compareElems(testCase.a, testCase.b); got != testCase.want {
				t.Errorf("compareElems(%v, %v) = %d, want %d", testCase.a, testCase.b, got, testCase.want)
			}
		})
	}
}

func TestCompareKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    sortKey
		b    sortKey
		want int
	}{
		{name: "both empty", want: 0},
		{
			name: "an empty key sorts first",
			b:    sortKey{numKey(0)},
			want: -1,
		},
		{
			name: "a prefix sorts before what extends it",
			a:    sortKey{numKey(0), textKey("x")},
			b:    sortKey{numKey(0), textKey("x"), numKey(0), numKey(0)},
			want: -1,
		},
		{
			name: "the first differing element decides",
			a:    sortKey{numKey(0), textKey("z")},
			b:    sortKey{numKey(1), textKey("a")},
			want: -1,
		},
		{
			name: "an arguments key sorts before an inputs key at the same position",
			a:    sortKey{numKey(0), numKey(7)},
			b:    sortKey{numKey(0), textKey("aaa")},
			want: -1,
		},
		{
			name: "identical keys",
			a:    sortKey{numKey(2), textKey("f")},
			b:    sortKey{numKey(2), textKey("f")},
			want: 0,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := compareKeys(testCase.a, testCase.b); got != testCase.want {
				t.Errorf("compareKeys(%v, %v) = %d, want %d", testCase.a, testCase.b, got, testCase.want)
			}

			if got := compareKeys(testCase.b, testCase.a); got != -testCase.want {
				t.Errorf("compareKeys(%v, %v) = %d, want %d", testCase.b, testCase.a, got, -testCase.want)
			}
		})
	}
}

// TestSortKeyChildDoesNotAlias pins the reason child copies: two children of one parent must not
// share a backing array, or the second silently overwrites the first.
func TestSortKeyChildDoesNotAlias(t *testing.T) {
	t.Parallel()

	parent := sortKey(nil).child(numKey(0), textKey("p"))

	first := parent.child(numKey(0))
	second := parent.child(numKey(1))

	if compareKeys(first, sortKey{numKey(0), textKey("p"), numKey(0)}) != 0 {
		t.Errorf("first child = %v, want [0 p 0]", first)
	}

	if compareKeys(second, sortKey{numKey(0), textKey("p"), numKey(1)}) != 0 {
		t.Errorf("second child = %v, want [0 p 1]", second)
	}

	if len(parent) != 2 {
		t.Errorf("parent grew to %v", parent)
	}
}

// TestSortKeyOrderIsTotalAndStable checks the whole ordering at once, on the mixed keys a real
// command line produces.
func TestSortKeyOrderIsTotalAndStable(t *testing.T) {
	t.Parallel()

	keys := []sortKey{
		{numKey(1), textKey("z")},
		{numKey(0), textKey("b")},
		{numKey(0), textKey("a")},
		{numKey(0), numKey(1)},
		{numKey(0), numKey(0)},
		{numKey(-5), textKey("neg")},
		{numKey(0), textKey("a"), numKey(0), numKey(0)},
	}

	slices.SortStableFunc(keys, compareKeys)

	want := []string{
		"#-5:neg",
		"#0:#0",
		"#0:#1",
		"#0:a",
		"#0:a:#0:#0",
		"#0:b",
		"#1:z",
	}

	for index, key := range keys {
		if got := cltRenderKey(key); got != want[index] {
			t.Errorf("sorted[%d] = %s, want %s", index, got, want[index])
		}
	}
}

// renderKey spells a sort key out for a failure message, marking numeric elements with "#" so that
// the numeric-before-string rule is visible in the output.
func cltRenderKey(key sortKey) string {
	parts := make([]string, 0, len(key))

	for _, elem := range key {
		if elem.isText {
			parts = append(parts, elem.text)

			continue
		}

		parts = append(parts, "#"+strconv.FormatInt(elem.num, argRadix))
	}

	return strings.Join(parts, ":")
}
