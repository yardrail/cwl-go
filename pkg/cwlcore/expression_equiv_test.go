package cwlcore

import (
	"reflect"
	"testing"
)

// minimumCorpusSize keeps the equivalence property from silently degrading
// into a smoke test if entries are ever removed.
const minimumCorpusSize = 30

// referenceCorpus is the shared body of parameter references the native and
// JavaScript evaluation paths are held to agree on. Every entry is a legal
// parameter reference under the spec's BNF, resolvable against testContext.
//
// Keep the values in testInputs canonical — int64 for integers, non-integral
// float64 — so that a difference here is a real disagreement rather than an
// artefact of JSON having one number type where Go has fifteen.
func referenceCorpus() []string {
	return []string{
		// The literal and the bare root symbols.
		refNull,
		refInputs,
		refSelf,
		"$(runtime)",

		// Field segments over every JSON type.
		refNumber,
		refFloat,
		refString,
		refBool,
		refNullField,
		refArray,
		"$(inputs.empty)",
		refRecord,
		"$(inputs.nested)",

		// Bracketed field segments, including keys no dotted form can reach.
		"$(inputs['n'])",
		`$(inputs["n"])`,
		"$(inputs['a b'])",
		`$(inputs["a b"])`,
		`$(inputs["quo\"te"])`,
		`$(inputs['quo\'te'])`,

		// Unicode identifiers.
		"$(inputs.файл)",
		"$(inputs['файл'])",

		// Index segments.
		"$(inputs.arr[0])",
		"$(inputs.arr[1])",
		"$(inputs.arr[2])",

		// .length as the final segment on a list, and the ordinary field named
		// length that must not be special-cased on an object.
		"$(inputs.arr.length)",
		"$(inputs.empty.length)",
		"$(inputs['arr'].length)",
		"$(inputs.length)",
		"$(inputs['length'])",

		// Deep chains mixing all three segment forms.
		"$(inputs.rec.a)",
		"$(inputs.rec['b'])",
		"$(inputs.nested.list)",
		"$(inputs.nested.list[0])",
		"$(inputs.nested.list[0].deep)",
		"$(inputs.nested['list'][0][\"deep\"])",

		// The runtime block.
		refOutdir,
		"$(runtime.tmpdir)",
		refCores,
		"$(runtime.ram)",
		"$(runtime['outdir'])",
	}
}

// TestParameterReferenceEquivalence asserts the spec's linking property
// between the two evaluation modes: parameter reference results "must be
// identical whether implemented by Javascript evaluation or some other means".
//
// It is deliberately one test over one corpus rather than two suites. Each
// entry is evaluated twice — once by the native grammar walker, once by goja —
// and the two results are compared both as values and as the text they
// interpolate to.
func TestParameterReferenceEquivalence(t *testing.T) {
	t.Parallel()

	corpus := referenceCorpus()
	if len(corpus) < minimumCorpusSize {
		t.Fatalf("corpus has %d expressions, want at least %d", len(corpus), minimumCorpusSize)
	}

	scripted := NewEvaluator(WithJS(nil))

	for _, expr := range corpus {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			checkEquivalent(t, scripted, expr, testContext)
		})
	}
}

// checkEquivalent evaluates one reference both ways and compares. context is a
// factory rather than a value so that each subtest gets its own, which parallel
// subtests need and which stops one path's result aliasing the other's input.
func checkEquivalent(t *testing.T, scripted *Evaluator, expr string, context func() *EvalContext) {
	t.Helper()

	body := expr[len("$"):]

	want, err := evalParamRef(body, context())
	if err != nil {
		t.Fatalf("native evaluation of %q returned error: %v", expr, err)
	}

	got, err := scripted.evalJSExpr(body, context())
	if err != nil {
		t.Fatalf("javascript evaluation of %q returned error: %v", expr, err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q: javascript = %#v, native = %#v", expr, got, want)
	}

	gotText, wantText := interpolatedText(got), interpolatedText(want)
	if gotText != wantText {
		t.Errorf("%q interpolates to %q via javascript and %q natively", expr, gotText, wantText)
	}
}

// TestParameterReferenceEquivalenceThroughEval repeats the property one level
// up, through the public API, for both the whole-string and the interpolated
// form of every corpus entry. An Evaluator built WithJS tries the native path
// first, so this pins that the fast path does not change what callers observe.
func TestParameterReferenceEquivalenceThroughEval(t *testing.T) {
	t.Parallel()

	native := NewEvaluator()
	scripted := NewEvaluator(WithJS(nil))

	for _, expr := range referenceCorpus() {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			for _, form := range []string{expr, "<" + expr + ">"} {
				checkEvalAgrees(t, native, scripted, form, testContext)
			}
		})
	}
}

// checkEvalAgrees compares the public Eval of one field value with and without
// the JavaScript engine available.
func checkEvalAgrees(t *testing.T, native, scripted *Evaluator, form string, context func() *EvalContext) {
	t.Helper()

	want, err := native.Eval(form, context())
	if err != nil {
		t.Fatalf("Eval(%q) without javascript returned error: %v", form, err)
	}

	got, err := scripted.Eval(form, context())
	if err != nil {
		t.Fatalf("Eval(%q) with javascript returned error: %v", form, err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Eval(%q): with javascript = %#v, without = %#v", form, got, want)
	}
}

// filesystemCorpus is the parameter references the typed File and Directory
// values must resolve identically through both paths. It covers every field of
// both records, the absent-versus-zero cases, nil-versus-empty listings, and
// the recursion through secondaryFiles and listing.
func filesystemCorpus() []string {
	return []string{
		// Every field of a fully populated File.
		"$(inputs.file.class)",
		"$(inputs.file.location)",
		"$(inputs.file.path)",
		"$(inputs.file.basename)",
		"$(inputs.file.dirname)",
		"$(inputs.file.nameroot)",
		"$(inputs.file.nameext)",
		"$(inputs.file.checksum)",
		"$(inputs.file.format)",
		"$(inputs.file.size)",
		"$(inputs.file.contents)",
		"$(inputs.file.secondaryFiles)",
		"$(inputs.file)",

		// Recursion through secondaryFiles, including a Directory inside one.
		"$(inputs.file.secondaryFiles.length)",
		"$(inputs.file.secondaryFiles[0])",
		"$(inputs.file.secondaryFiles[0].basename)",
		"$(inputs.file.secondaryFiles[1].class)",
		"$(inputs.file.secondaryFiles[1].listing[0].basename)",
		`$(inputs.file["secondaryFiles"][1].listing)`,

		// A File with nothing but a basename, and one whose optional fields
		// are set to their zero values.
		"$(inputs.bare)",
		"$(inputs.bare.basename)",
		"$(inputs.zeroed.size)",
		"$(inputs.zeroed.contents)",
		"$(inputs.zeroed)",

		// Every field of a Directory, and the three states of its listing.
		"$(inputs.dir.class)",
		"$(inputs.dir.location)",
		"$(inputs.dir.path)",
		"$(inputs.dir.basename)",
		"$(inputs.dir.listing)",
		"$(inputs.dir.listing[0].basename)",
		"$(inputs.dir.listing.length)",
		"$(inputs.dir)",
		"$(inputs.unread)",
		"$(inputs.emptydir)",
		"$(inputs.emptydir.listing)",
		"$(inputs.emptydir.listing.length)",

		// Typed values reached through the surrounding collections.
		"$(inputs.union)",
		"$(inputs.union[0].basename)",
		"$(inputs.union[1].class)",
		"$(inputs.record.inner.basename)",
		"$(inputs.byvalue.basename)",
		"$(inputs.list[0].path)",
		"$(inputs)",

		// self, which is what an outputEval or a secondaryFiles expression
		// reads.
		"$(self)",
		"$(self.length)",
		"$(self[0])",
		"$(self[0].basename)",
		"$(self[0].size)",
	}
}

// filesystemContext is a parameter context built entirely from the engine's
// own typed values, with no map anywhere.
func filesystemContext() *EvalContext {
	return &EvalContext{
		Inputs: map[string]any{
			"file":   testFile(),
			"bare":   &File{Basename: readsName},
			"zeroed": &File{Basename: readsName, Size: NewOptInt(0), Contents: NewOptString("")},
			"dir": &Directory{
				Location: "file:///d",
				Path:     "/d",
				Basename: "d",
				Listing:  []FileOrDirectory{&File{Basename: notesName, Path: "/d/" + notesName}},
			},
			"unread":   &Directory{Basename: "u", Path: "/u"},
			"emptydir": &Directory{Basename: "e", Path: "/e", Listing: make([]FileOrDirectory, 0)},
			"union":    []FileOrDirectory{&File{Basename: notesName}, &Directory{Basename: auxName}},
			"record":   map[string]any{innerKey: &File{Basename: notesName}},
			"byvalue":  File{Basename: notesName},
			"list":     []any{&File{Basename: readsName, Path: readsPath}},
		},
		Self: []any{&File{Basename: selfName, Size: NewOptInt(7)}},
	}
}

// TestFilesystemValueEquivalence extends the specification's linking property
// to the typed values. The native walker reads a *File through its Go fields
// and the engine reads the JSON the encoder produced from it, by two entirely
// separate routes, and they have to agree — otherwise a workflow would behave
// differently depending only on whether InlineJavascriptRequirement happened to
// be in scope.
func TestFilesystemValueEquivalence(t *testing.T) {
	t.Parallel()

	scripted := NewEvaluator(WithJS(nil))

	for _, expr := range filesystemCorpus() {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			checkEquivalent(t, scripted, expr, filesystemContext)
		})
	}
}

// TestFilesystemValueEquivalenceThroughEval repeats it through the public API,
// in both the whole-string and the interpolated form.
func TestFilesystemValueEquivalenceThroughEval(t *testing.T) {
	t.Parallel()

	native := NewEvaluator()
	scripted := NewEvaluator(WithJS(nil))

	for _, expr := range filesystemCorpus() {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			for _, form := range []string{expr, "<" + expr + ">"} {
				checkEvalAgrees(t, native, scripted, form, filesystemContext)
			}
		})
	}
}
