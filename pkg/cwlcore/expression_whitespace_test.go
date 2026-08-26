package cwlcore

import (
	"reflect"
	"testing"
)

// Whitespace around a fragment, which two callers need to disagree about.
//
// The specification's rule — "If the value of a field has no leading or
// trailing non-whitespace characters around a parameter reference, the
// effective value of the field becomes the value of the referenced parameter,
// preserving the return type" — decides whether a value keeps the referenced
// value's type. It says nothing about what happens to the whitespace itself,
// and the two answers are both needed:
//
//   - A document field's surrounding whitespace is punctuation. A field written
//     as a YAML block scalar always ends in a newline, and it must still count
//     as "no trailing non-whitespace characters". Eval strips first.
//   - A Dirent entry is file content. Its trailing newline is a byte the
//     conformance suite compares. EvalContent does not strip.
//
// No single test on the scanned string satisfies both: tolerating whitespace in
// the whole-string test would make "${...}\n" typed and drop the newline, and
// not tolerating it without a strip would make a block scalar interpolate.
// Splitting the strip from the test is what settles it, which is also where the
// reference implementation puts the choice — do_eval's strip_whitespace, passed
// false by initialworkdir.py.

// jsQuote mirrors tests/js-quote.cwl: a Dirent entry that is one function body
// and nothing else, followed by the block scalar's newline.
const (
	jsQuoteEntry = "${return 'quote \"' + inputs.quote + '\"'}\n"
	jsQuoteWant  = "quote \"Hello\"\n"
)

// quoteContext is the input object tests/js-quote.cwl evaluates against.
func quoteContext() *EvalContext {
	return &EvalContext{Inputs: map[string]any{"quote": "Hello"}}
}

// TestEvalStripsFieldWhitespaceAndKeepsTheType covers the two rows a document
// field needs: padding around a lone reference still yields the referenced
// value with its type intact, however the padding is spelled.
func TestEvalStripsFieldWhitespaceAndKeepsTheType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want any
		name string
		expr string
	}{
		{name: "spaces on both sides", expr: "  " + refNumber + "  ", want: int64(3)},
		{
			name: "a block scalar's trailing newline",
			expr: refRecord + "\n",
			want: map[string]any{"a": int64(1), "b": int64(2)},
		},
		{name: "tabs on both sides", expr: "\t" + refArray + "\t", want: []any{int64(1), int64(2), int64(3)}},
		{name: "mixed padding", expr: " \n\t" + refBool + "\t\n ", want: true},
		{name: "padding around a null", expr: "  " + refNullField + "  ", want: nil},
		{name: "no padding at all", expr: refNumber, want: int64(3)},
	}

	evaluator := NewEvaluator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.Eval(tc.expr, testContext())
			if err != nil {
				t.Fatalf("Eval(%q) returned error: %v", tc.expr, err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Eval(%q) = %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEvalContentKeepsEveryByte covers the two rows a Dirent entry needs. Both
// are byte counts the conformance suite compares against a staged file, so the
// assertion reports lengths when it fails.
func TestEvalContentKeepsEveryByte(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want string
	}{
		// A fragment with literal text around it. The literal run alone is
		// enough to reach interpolation, so only the trailing newline is at
		// stake here.
		{
			name: "a literal prefix and a trailing newline",
			expr: "CONFIGVAR=" + refString + "\n",
			want: "CONFIGVAR=a\n",
		},

		// The case a whitespace-tolerant whole-string test gets wrong: nothing
		// precedes the fragment and only a newline follows, so the fragment
		// looks like the whole value until the newline is counted as content.
		{name: "nothing but a fragment and a newline", expr: refString + "\n", want: "a\n"},
		{name: "nothing but a fragment and a space", expr: refNumber + " ", want: "3 "},
		{name: "a leading newline", expr: "\n" + refString, want: "\na"},
		{name: "interior whitespace between fragments", expr: refString + " \t " + refNumber, want: "a \t 3"},
		{name: "whitespace around every fragment", expr: " " + refString + "  " + refNumber + " ", want: " a  3 "},
		{name: "an escape is not trimmed either", expr: `\` + refNumber + "\n", want: refNumber + "\n"},
	}

	evaluator := NewEvaluator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.EvalContent(tc.expr, testContext())
			if err != nil {
				t.Fatalf("EvalContent(%q) returned error: %v", tc.expr, err)
			}

			text, ok := got.(string)
			if !ok {
				t.Fatalf("EvalContent(%q) = %#v (%T), want a string", tc.expr, got, got)
			}

			if text != tc.want {
				t.Errorf("EvalContent(%q) = %q (%d bytes), want %q (%d bytes)",
					tc.expr, text, len(text), tc.want, len(tc.want))
			}
		})
	}
}

// TestEvalContentStagesTheConformanceDirents pins the two entries the suite
// measures byte for byte: escaping_expression_no_extra_quotes, whose whole
// entry is one function body plus a newline, and the CONFIGVAR shape.
func TestEvalContentStagesTheConformanceDirents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "js-quote.cwl", expr: jsQuoteEntry, want: jsQuoteWant},
		{name: "a CONFIGVAR entry", expr: "CONFIGVAR=$(inputs.quote)\n", want: "CONFIGVAR=Hello\n"},
	}

	evaluator := NewEvaluator(WithJS(nil))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.EvalContent(tc.expr, quoteContext())
			if err != nil {
				t.Fatalf("EvalContent(%q) returned error: %v", tc.expr, err)
			}

			text, ok := got.(string)
			if !ok {
				t.Fatalf("EvalContent(%q) = %#v (%T), want a string", tc.expr, got, got)
			}

			if text != tc.want {
				t.Errorf("EvalContent(%q) = %q (%d bytes), want %q (%d bytes)",
					tc.expr, text, len(text), tc.want, len(tc.want))
			}
		})
	}
}

// TestEvalContentStillPreservesTypeForAWholeFragment checks that not stripping
// is not the same as never preserving a type. An entry that is exactly one
// fragment, with nothing after it at all, still yields the typed value — which
// is what lets a Dirent entry evaluate to an object and be staged as JSON.
func TestEvalContentStillPreservesTypeForAWholeFragment(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want any
		name string
		expr string
	}{
		{name: "an object", expr: refRecord, want: map[string]any{"a": int64(1), "b": int64(2)}},
		{name: "an array", expr: refArray, want: []any{int64(1), int64(2), int64(3)}},
		{name: "a number", expr: refNumber, want: int64(3)},

		// A newline after the fragment is content, so this one interpolates
		// instead — the exact difference from Eval, spelled out.
		{name: "the same object with a newline", expr: refRecord + "\n", want: `{"a": 1, "b": 2}` + "\n"},
	}

	evaluator := NewEvaluator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.EvalContent(tc.expr, testContext())
			if err != nil {
				t.Fatalf("EvalContent(%q) returned error: %v", tc.expr, err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EvalContent(%q) = %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEvalLeavesAnExpressionlessStringAlone pins the case both entry points
// share: a string carrying no fragment is data, and comes back exactly as
// written whichever of them is asked.
func TestEvalLeavesAnExpressionlessStringAlone(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"  padded  ", "\ntext\n", plainText, "\t", " "} {
		stripped, err := NewEvaluator().Eval(expr, testContext())
		if err != nil {
			t.Fatalf("Eval(%q) returned error: %v", expr, err)
		}

		if stripped != expr {
			t.Errorf("Eval(%q) = %#v, want it unchanged", expr, stripped)
		}

		verbatim, err := NewEvaluator().EvalContent(expr, testContext())
		if err != nil {
			t.Fatalf("EvalContent(%q) returned error: %v", expr, err)
		}

		if verbatim != expr {
			t.Errorf("EvalContent(%q) = %#v, want it unchanged", expr, verbatim)
		}
	}
}

// TestEvalAndEvalContentAgreeWhereNothingIsStripped is the property that keeps
// the pair honest: they are the same function on any value with no surrounding
// whitespace to disagree about.
func TestEvalAndEvalContentAgreeWhereNothingIsStripped(t *testing.T) {
	t.Parallel()

	exprs := []string{
		refNumber, refRecord, refArray, plainText,
		"x" + refString + "y", refString + "-" + refNumber, `\` + refNumber,
	}

	evaluator := NewEvaluator()

	for _, expr := range exprs {
		stripped, err := evaluator.Eval(expr, testContext())
		if err != nil {
			t.Fatalf("Eval(%q) returned error: %v", expr, err)
		}

		verbatim, err := evaluator.EvalContent(expr, testContext())
		if err != nil {
			t.Fatalf("EvalContent(%q) returned error: %v", expr, err)
		}

		if !reflect.DeepEqual(stripped, verbatim) {
			t.Errorf("Eval(%q) = %#v but EvalContent = %#v", expr, stripped, verbatim)
		}
	}
}
