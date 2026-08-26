package cwlcore

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// scanCase is one scanner expectation: the input, the substring the first
// window should cover, and whether that window is an escape rather than an
// expression fragment. An empty want means no window at all.
type scanCase struct {
	name   string
	src    string
	want   string
	escape bool
	found  bool
}

func scanCasesWithoutFragments() []scanCase {
	return []scanCase{
		{name: "nothing at all", src: ""},
		{name: "no fragment", src: plainText},
		{name: "lone dollar", src: "cost is $"},
		{name: "dollar then a letter", src: "a $b c"},
		{name: "trailing backslash", src: `ends with \`},
	}
}

func scanCasesWithNesting() []scanCase {
	return []scanCase{
		{name: "simple reference", src: exprSample, want: exprSample, found: true},
		{name: "embedded reference", src: "a" + exprSample + "b", want: exprSample, found: true},
		{name: "a body fragment", src: exprBody, want: exprBody, found: true},
		{name: "first of several", src: "$(a)$(b)", want: "$(a)", found: true},
		{name: "double dollar", src: "$$(a)", want: "$(a)", found: true},

		// The closing delimiter is the matching one, not the first one.
		{name: "nested parens", src: "$(f(g(1), h(2)))", want: "$(f(g(1), h(2)))", found: true},
		{
			name:  "nested braces",
			src:   "${ if (x) { return 1; } return 2; }",
			want:  "${ if (x) { return 1; } return 2; }",
			found: true,
		},
		{name: "object literal", src: "$({a: {b: [1, 2]}})", want: "$({a: {b: [1, 2]}})", found: true},
		{
			name:  "function literal in an expression",
			src:   "$(f(function () { return 1; }))",
			want:  "$(f(function () { return 1; }))",
			found: true,
		},
	}
}

func scanCasesWithStrings() []scanCase {
	return []scanCase{
		{name: "paren in single quotes", src: `$(x + ')')`, want: `$(x + ')')`, found: true},
		{name: "paren in double quotes", src: `$(x + ")")`, want: `$(x + ")")`, found: true},
		{name: "brace in single quotes", src: `${ return '}'; }`, want: `${ return '}'; }`, found: true},
		{name: "brace in double quotes", src: exprBraceString, want: exprBraceString, found: true},
		{name: "escaped quote in a string", src: `$('it\'s (a) test')`, want: `$('it\'s (a) test')`, found: true},
		{name: "escaped double quote", src: `$("say \"hi)\"")`, want: `$("say \"hi)\"")`, found: true},
		{name: "quote inside another quote", src: `$("it's ) fine")`, want: `$("it's ) fine")`, found: true},
		{name: "backslash before the closing quote", src: `$('a\\')`, want: `$('a\\')`, found: true},
	}
}

func scanCasesWithEscapes() []scanCase {
	return []scanCase{
		{name: "escaped reference opener", src: `\$(a)`, want: `\$(`, escape: true, found: true},
		{name: "escaped body opener", src: `\${a}`, want: `\${`, escape: true, found: true},
		{name: "escaped dollar alone", src: `\$x`, want: `\$`, escape: true, found: true},
		{name: "escaped backslash", src: `\\`, want: `\\`, escape: true, found: true},
		{name: "escape before text", src: `\n and $(a)`, want: `\n`, escape: true, found: true},
		{name: "escaped multibyte rune", src: `\é$(a)`, want: `\é`, escape: true, found: true},
	}
}

func TestScanFragment(t *testing.T) {
	t.Parallel()

	groups := map[string][]scanCase{
		"without fragments": scanCasesWithoutFragments(),
		"with nesting":      scanCasesWithNesting(),
		"with strings":      scanCasesWithStrings(),
		"with escapes":      scanCasesWithEscapes(),
	}

	for group, cases := range groups {
		t.Run(group, func(t *testing.T) {
			t.Parallel()

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					checkScanCase(t, tc)
				})
			}
		})
	}
}

// checkScanCase runs one scanner expectation.
func checkScanCase(t *testing.T, tc scanCase) {
	t.Helper()

	window, found, err := scanFragment(tc.src)
	if err != nil {
		t.Fatalf("scanFragment(%q) returned error: %v", tc.src, err)
	}

	if found != tc.found {
		t.Fatalf("scanFragment(%q) found = %v, want %v", tc.src, found, tc.found)
	}

	if !found {
		return
	}

	if got := tc.src[window.start:window.end]; got != tc.want {
		t.Errorf("scanFragment(%q) = %q, want %q", tc.src, got, tc.want)
	}

	if window.escape != tc.escape {
		t.Errorf("scanFragment(%q) escape = %v, want %v", tc.src, window.escape, tc.escape)
	}
}

func TestScanFragmentRejectsUnterminated(t *testing.T) {
	t.Parallel()

	cases := []string{
		"$(",
		"${",
		"$(inputs.x",
		"${ return 1;",
		"$(f(g())",
		`$('unclosed`,
		`$("unclosed)`,
		"${ if (x) { return 1; }",
		`$(a + "b\")`,
	}

	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			t.Parallel()

			_, found, err := scanFragment(src)
			if !errors.Is(err, ErrExpressionSyntax) {
				t.Fatalf("scanFragment(%q) err = %v, found = %v, want ErrExpressionSyntax", src, err, found)
			}
		})
	}
}

// TestScanFragmentTerminates is a crude guard against the scanner's state
// machine failing to make progress: every input must be consumed in a bounded
// number of steps regardless of how the delimiters and quotes are arranged,
// and any window it does report must lie inside the input.
func TestScanFragmentTerminates(t *testing.T) {
	t.Parallel()

	const repeats = 200

	cases := []string{
		strings.Repeat("$", repeats),
		strings.Repeat(`\`, repeats),
		strings.Repeat("$(", repeats),
		strings.Repeat("'", repeats),
		strings.Repeat(`$('\`, repeats),
		strings.Repeat("${$(", repeats),
		strings.Repeat(`$("}`, repeats),
	}

	for i, src := range cases {
		t.Run("case"+strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			window, found, err := scanFragment(src)
			if err != nil || !found {
				return
			}

			if window.start < 0 || window.end > len(src) || window.start >= window.end {
				t.Errorf("scanFragment(%q) window = %+v, want it inside the input", src, window)
			}
		})
	}
}

// TestScanFragmentWalksWholeString exercises the loop the interpolator runs:
// repeatedly scanning the remaining text must consume the whole input.
func TestScanFragmentWalksWholeString(t *testing.T) {
	t.Parallel()

	const src = `a$(x)b\$(y)c${return "}";}d\\e$(f("(") + ')')g`

	want := []string{"$(x)", `\$(`, `${return "}";}`, `\\`, `$(f("(") + ')')`}

	rest := src
	got := make([]string, 0, len(want))

	for {
		window, found, err := scanFragment(rest)
		if err != nil {
			t.Fatalf("scanFragment(%q) returned error: %v", rest, err)
		}

		if !found {
			break
		}

		got = append(got, rest[window.start:window.end])
		rest = rest[window.end:]
	}

	if len(got) != len(want) {
		t.Fatalf("scanned %q, want %q", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fragment %d = %q, want %q", i, got[i], want[i])
		}
	}
}
