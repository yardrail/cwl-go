package salad

import "testing"

// expandTestContext is the context the expansion tables resolve against: one
// namespace prefix and one vocabulary term.
func expandTestContext() *Context {
	c := newContext()
	c.namespaces["acid"] = testAcidNS
	c.putVocab(testRed, testAcidRed)
	c.finish()

	return c
}

func TestExpandURLFollowsLinkResolutionRules(t *testing.T) {
	t.Parallel()

	const base = testBaseURI

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "a fragment replaces the base fragment", in: "#three", want: base + "#three"},
		{name: "a relative path replaces the last segment", in: testOne, want: "http://example.com/one"},
		{name: "a relative path keeps its fragment", in: "four#five", want: "http://example.com/four#five"},
		{name: msgPrefixExpands, in: testAcidSix, want: testAcidNS + "six"},
		{name: "an absolute URI is left alone", in: base + "/zero", want: base + "/zero"},
		{name: "a template expression is left alone", in: "$(inputs.x)", want: "$(inputs.x)"},
	}

	ctx := expandTestContext()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ctx.ExpandURL(tc.in, base); got != tc.want {
				t.Errorf("ExpandURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExpandIdentifierFollowsIdentifierResolutionRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		base string
		want string
	}{
		{
			name: "a parent relative name becomes the base fragment",
			in:   testOne,
			base: testBaseURI,
			want: testBaseOne,
		},
		{
			name: "a parent relative name extends an existing fragment",
			in:   testTwo,
			base: testBaseOne,
			want: testBaseOne + "/two",
		},
		{
			name: "a leading hash replaces the fragment",
			in:   "#three",
			base: testBaseOne,
			want: testBaseURI + "#three",
		},
		{
			name: "a relative path with a fragment resolves as a link",
			in:   "four#five",
			base: testBaseOne,
			want: "http://example.com/four#five",
		},
		{
			name: msgPrefixExpands,
			in:   testAcidSix,
			base: testBaseOne,
			want: testAcidNS + "six",
		},
		{
			name: "an absolute URI is left alone",
			in:   testBaseURI,
			base: "",
			want: testBaseURI,
		},
	}

	ctx := expandTestContext()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ctx.ExpandIdentifier(tc.in, tc.base); got != tc.want {
				t.Errorf("ExpandIdentifier(%q, %q) = %q, want %q", tc.in, tc.base, got, tc.want)
			}
		})
	}
}

func TestExpandVocabTerm(t *testing.T) {
	t.Parallel()

	ctx := expandTestContext()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "a term stays a term", in: testRed, want: testRed},
		{name: "a term's IRI collapses back to the term", in: testAcidRed, want: testRed},
		{name: "an unknown IRI is left alone", in: testAcidNS + "blue", want: testAcidNS + "blue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ctx.ExpandVocabTerm(tc.in, testBaseURI); got != tc.want {
				t.Errorf("ExpandVocabTerm(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveFieldName(t *testing.T) {
	t.Parallel()

	ctx := expandTestContext()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "a vocabulary term is left alone", in: testRed, want: testRed},
		{name: "a term's IRI becomes the term", in: testAcidRed, want: testRed},
		{
			name: "an unknown absolute URI is left alone",
			in:   "http://example.com/three",
			want: "http://example.com/three",
		},
		{name: msgPrefixExpands, in: "acid:four", want: testAcidNS + "four"},
		{name: "an unknown short name is left alone", in: "form", want: "form"},
		{name: "a directive is left alone", in: "$base", want: "$base"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ctx.resolveFieldName(tc.in); got != tc.want {
				t.Errorf("resolveFieldName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSubscopeExtendsTheIdentifierScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		base string
		sub  string
		want string
	}{
		{
			name: "a base with a fragment gains a scope level",
			base: testBaseOne,
			sub:  testSub,
			want: testBaseOne + "/" + testSub,
		},
		{
			name: "a base with no fragment gains one",
			base: testBaseURI,
			sub:  testSub,
			want: testBaseURI + "#" + testSub,
		},
		{name: "an empty subscope changes nothing", base: testBaseURI, sub: "", want: testBaseURI},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := scopeSubscope(tc.base, tc.sub); got != tc.want {
				t.Errorf("scopeSubscope(%q, %q) = %q, want %q", tc.base, tc.sub, got, tc.want)
			}
		})
	}
}

func TestHasScheme(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{in: "http://example.com", want: true},
		{in: "file:///a/b", want: true},
		{in: testAcidSix, want: true},
		{in: "a+b-c.d:x", want: true},
		{in: "1abc:x", want: false},
		{in: ":x", want: false},
		{in: testPlain, want: false},
		{in: "a/b:c", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := hasScheme(tc.in); got != tc.want {
				t.Errorf("hasScheme(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
