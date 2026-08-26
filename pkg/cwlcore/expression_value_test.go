package cwlcore

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Fixture strings shared by the filesystem tests.
const (
	readsName = "reads.bam"
	readsPath = "/data/reads.bam"
	readsRoot = "reads"
	readsExt  = ".bam"
	indexName = "reads.bam.bai"
	indexExt  = ".bai"
	notesName = "notes.txt"
	notesRoot = "notes"
	notesExt  = ".txt"
	auxName   = "aux"
	selfName  = "self.txt"
	innerKey  = "inner"
	someText  = "hello"
	notAnInt  = "big"
)

// testFile is a File with every field populated, including a secondaryFiles
// list that mixes a File and a Directory so the recursion is exercised.
func testFile() *File {
	return &File{
		Location: "file:///data/reads.bam",
		Path:     readsPath,
		Basename: readsName,
		Dirname:  "/data",
		Nameroot: readsRoot,
		Nameext:  readsExt,
		Checksum: "sha1$deadbeef",
		Format:   "http://edamontology.org/format_2572",
		Size:     NewOptInt(1024),
		Contents: NewOptString(someText),
		SecondaryFiles: []FileOrDirectory{
			&File{Basename: indexName, Path: "/data/" + indexName, Nameroot: readsName, Nameext: indexExt},
			&Directory{
				Basename: auxName,
				Path:     "/data/" + auxName,
				Listing:  []FileOrDirectory{&File{Basename: notesName, Nameroot: notesRoot, Nameext: notesExt}},
			},
		},
	}
}

// testFileObject is testFile in the shape an expression reads.
func testFileObject() map[string]any {
	return map[string]any{
		keyClass:    ClassFile,
		keyLocation: "file:///data/reads.bam",
		keyPath:     readsPath,
		keyBasename: readsName,
		keyDirname:  "/data",
		keyNameroot: readsRoot,
		keyNameext:  readsExt,
		keyChecksum: "sha1$deadbeef",
		keyFormat:   "http://edamontology.org/format_2572",
		keySize:     int64(1024),
		keyContents: someText,
		keySecondaryFiles: []any{
			map[string]any{
				keyClass:    ClassFile,
				keyBasename: indexName,
				keyNameroot: readsName,
				keyNameext:  indexExt,
				keyPath:     "/data/" + indexName,
			},
			map[string]any{
				keyClass:    ClassDirectory,
				keyBasename: auxName,
				keyPath:     "/data/" + auxName,
				keyListing: []any{
					map[string]any{
						keyClass: ClassFile, keyBasename: notesName,
						keyNameroot: notesRoot, keyNameext: notesExt,
					},
				},
			},
		},
	}
}

// notesFile is a minimal but fully-named File, and notesObject is what it
// renders as. The two are a pair so that the nesting cases below assert the
// same object wherever the value is reached from.
func notesFile() *File {
	return &File{Basename: notesName, Nameroot: notesRoot, Nameext: notesExt}
}

func notesObject() map[string]any {
	return map[string]any{
		keyClass: ClassFile, keyBasename: notesName, keyNameroot: notesRoot, keyNameext: notesExt,
	}
}

func TestToExpressionValueFile(t *testing.T) {
	t.Parallel()

	got := ToExpressionValue(testFile())
	if !reflect.DeepEqual(got, testFileObject()) {
		t.Errorf("ToExpressionValue(File) = %#v,\nwant %#v", got, testFileObject())
	}
}

// TestToExpressionValueOmitsAbsentFields is the absent-versus-zero guarantee:
// a field the runtime never filled in must not surface as 0 or "", because
// that would fabricate a measurement.
func TestToExpressionValueOmitsAbsentFields(t *testing.T) {
	t.Parallel()

	bare := ToExpressionValue(&File{Basename: readsName, Nameroot: readsRoot, Nameext: readsExt})

	object, ok := bare.(map[string]any)
	if !ok {
		t.Fatalf("ToExpressionValue(File) = %#v, want an object", bare)
	}

	want := map[string]any{
		keyClass: ClassFile, keyBasename: readsName, keyNameroot: readsRoot, keyNameext: readsExt,
	}
	if !reflect.DeepEqual(object, want) {
		t.Errorf("a bare File rendered as %#v, want %#v", object, want)
	}

	// The zero value of each optional field is a legal value, so setting it
	// must put the key back.
	zeroed := ToExpressionValue(&File{
		Basename: readsName,
		Nameroot: readsRoot,
		Nameext:  readsExt,
		Size:     NewOptInt(0),
		Contents: NewOptString(""),
	})

	wantZeroed := map[string]any{
		keyClass:    ClassFile,
		keyBasename: readsName,
		keyNameroot: readsRoot,
		keyNameext:  readsExt,
		keySize:     int64(0),
		keyContents: "",
	}

	if !reflect.DeepEqual(zeroed, wantZeroed) {
		t.Errorf("an empty File rendered as %#v, want %#v", zeroed, wantZeroed)
	}
}

// TestToExpressionValueDirectoryListing pins nil-versus-empty: an unread
// listing is absent, an empty directory is [].
func TestToExpressionValueDirectoryListing(t *testing.T) {
	t.Parallel()

	unread := ToExpressionValue(&Directory{Basename: auxName})

	unreadObject, ok := unread.(map[string]any)
	if !ok {
		t.Fatalf("ToExpressionValue(Directory) = %#v, want an object", unread)
	}

	if _, present := unreadObject[keyListing]; present {
		t.Errorf("an unread Directory rendered %s, want it omitted", keyListing)
	}

	empty := ToExpressionValue(&Directory{Basename: auxName, Listing: make([]FileOrDirectory, 0)})

	want := map[string]any{
		keyClass:    ClassDirectory,
		keyBasename: auxName,
		keyListing:  make([]any, 0),
	}

	if !reflect.DeepEqual(empty, want) {
		t.Errorf("an empty Directory rendered as %#v, want %#v", empty, want)
	}
}

func TestToExpressionValueNesting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		want  any
		name  string
	}{
		{
			name:  "a value rather than a pointer",
			value: File{Basename: notesName, Nameroot: notesRoot, Nameext: notesExt},
			want:  notesObject(),
		},
		{
			name:  "a union slice",
			value: []FileOrDirectory{notesFile(), &Directory{Basename: auxName}},
			want: []any{
				notesObject(),
				map[string]any{keyClass: ClassDirectory, keyBasename: auxName},
			},
		},
		{
			name:  "inside a record",
			value: map[string]any{innerKey: notesFile()},
			want:  map[string]any{innerKey: notesObject()},
		},
		{
			name:  "inside a list",
			value: []any{int64(1), notesFile()},
			want:  []any{int64(1), notesObject()},
		},
		{
			name:  "a nil pointer becomes null",
			value: []FileOrDirectory{(*File)(nil)},
			want:  []any{nil},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ToExpressionValue(tc.value); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ToExpressionValue(%#v) = %#v, want %#v", tc.value, got, tc.want)
			}
		})
	}
}

// TestToExpressionValuePassesPlainDataThrough pins that the common case — a
// job object holding no filesystem values at all — is neither copied nor
// disturbed, since this runs on every resolved parameter reference.
func TestToExpressionValuePassesPlainDataThrough(t *testing.T) {
	t.Parallel()

	object := map[string]any{"a": int64(1), "b": []any{"x"}}
	if got := ToExpressionValue(object); !sameMap(got, object) {
		t.Error("ToExpressionValue copied an object that needed no conversion")
	}

	list := []any{int64(1), "two"}
	if got := ToExpressionValue(list); !sameSlice(got, list) {
		t.Error("ToExpressionValue copied a list that needed no conversion")
	}

	for _, scalar := range []any{nil, int64(3), "s", true, 2.5} {
		if got := ToExpressionValue(scalar); !reflect.DeepEqual(got, scalar) {
			t.Errorf("ToExpressionValue(%#v) = %#v", scalar, got)
		}
	}
}

// TestToExpressionValueLeavesTheOriginalAlone pins that conversion copies
// rather than editing the caller's job object in place.
func TestToExpressionValueLeavesTheOriginalAlone(t *testing.T) {
	t.Parallel()

	file := &File{Basename: notesName}
	object := map[string]any{"f": file}

	converted := ToExpressionValue(object)
	if sameMap(converted, object) {
		t.Fatal("ToExpressionValue returned the caller's own map")
	}

	if object["f"] != any(file) {
		t.Errorf("the original object now holds %#v, want the File it started with", object["f"])
	}
}

// sameMap reports whether two values are the same map, not merely equal ones.
func sameMap(got, want any) bool {
	return reflect.ValueOf(got).UnsafePointer() == reflect.ValueOf(want).UnsafePointer()
}

// sameSlice reports whether two values share a backing array.
func sameSlice(got, want any) bool {
	return reflect.ValueOf(got).UnsafePointer() == reflect.ValueOf(want).UnsafePointer()
}

// TestFromExpressionValueRoundTrip is the property that matters most about the
// inverse: a typed value rendered for an expression and read back is the value
// it started as, absences included.
func TestFromExpressionValueRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
	}{
		{name: "a full File", value: testFile()},
		{name: "a bare File", value: &File{Basename: readsName}},
		{name: "an empty File", value: &File{Basename: readsName, Size: NewOptInt(0), Contents: NewOptString("")}},
		{name: "an unread Directory", value: &Directory{Basename: auxName, Path: "/d"}},
		{name: "an empty Directory", value: &Directory{Basename: auxName, Listing: make([]FileOrDirectory, 0)}},
		{
			name:  "a File with no secondary files",
			value: &File{Basename: readsName, SecondaryFiles: make([]FileOrDirectory, 0)},
		},
		{name: "a record around a File", value: map[string]any{innerKey: &File{Basename: notesName}}},
		{
			name:  "a bare top-level list",
			value: []any{testFile(), &Directory{Basename: auxName, Path: "/data/" + auxName}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := FromExpressionValue(ToExpressionValue(tc.value))
			if err != nil {
				t.Fatalf("FromExpressionValue returned error: %v", err)
			}

			if !reflect.DeepEqual(got, tc.value) {
				t.Errorf("round trip = %#v, want %#v", got, tc.value)
			}
		})
	}
}

func TestFromExpressionValuePassesOtherValuesThrough(t *testing.T) {
	t.Parallel()

	for _, value := range []any{nil, int64(3), "s", true, 2.5} {
		got, err := FromExpressionValue(value)
		if err != nil || !reflect.DeepEqual(got, value) {
			t.Errorf("FromExpressionValue(%#v) = %#v, %v", value, got, err)
		}
	}
}

func TestFromExpressionValueErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
	}{
		{name: "a size that is text", value: map[string]any{keyClass: ClassFile, keySize: notAnInt}},
		{name: "a fractional size", value: map[string]any{keyClass: ClassFile, keySize: 1.5}},
		{name: "a path that is a number", value: map[string]any{keyClass: ClassFile, keyPath: int64(7)}},
		{name: "contents that are a list", value: map[string]any{keyClass: ClassFile, keyContents: make([]any, 0)}},
		{name: "a listing that is not a list", value: map[string]any{keyClass: ClassDirectory, keyListing: int64(3)}},
		{
			name: "an entry with no class",
			value: map[string]any{
				keyClass:          ClassFile,
				keySecondaryFiles: []any{map[string]any{keyBasename: notesName}},
			},
		},
		{
			name:  "an entry that is not an object",
			value: map[string]any{keyClass: ClassFile, keySecondaryFiles: []any{int64(1)}},
		},
		{
			name:  "a bad value nested in a record",
			value: map[string]any{innerKey: map[string]any{keyClass: ClassFile, keySize: notAnInt}},
		},
		{
			name:  "a bad value nested in a list",
			value: []any{map[string]any{keyClass: ClassFile, keySize: notAnInt}},
		},
		{
			name: "a secondary file whose own fields do not typecheck",
			value: map[string]any{
				keyClass: ClassFile,
				keySecondaryFiles: []any{
					map[string]any{keyClass: ClassFile, keyBasename: notesName, keySize: notAnInt},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := FromExpressionValue(tc.value)
			if !errors.Is(err, ErrExpressionEval) {
				t.Fatalf("FromExpressionValue(%#v) = %#v, err = %v, want ErrExpressionEval", tc.value, got, err)
			}
		})
	}
}

// TestFromExpressionValueAcceptsJavaScriptNumbers covers the shape a size
// arrives in when an expression computed it: JavaScript has one number type,
// so a whole number may come back as a float.
func TestFromExpressionValueAcceptsJavaScriptNumbers(t *testing.T) {
	t.Parallel()

	got, err := FromExpressionValue(map[string]any{keyClass: ClassFile, keySize: 1024.0})
	if err != nil {
		t.Fatalf("FromExpressionValue returned error: %v", err)
	}

	file, ok := got.(*File)
	if !ok {
		t.Fatalf("FromExpressionValue = %#v, want a *File", got)
	}

	if !file.Size.IsSet() || file.Size.Int() != 1024 {
		t.Errorf("size = %s, want 1024", file.Size)
	}
}

// TestEvalFilesystemFields is the bug this whole file exists for: an
// expression reaching a property of the engine's own *File used to fail,
// because the evaluator only understood string-keyed maps.
func TestEvalFilesystemFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want any
		name string
		expr string
	}{
		{name: "basename", expr: "$(inputs.f.basename)", want: readsName},
		{name: "the staged path", expr: "$(inputs.f.path)", want: readsPath},
		{name: "nameroot", expr: "$(inputs.f.nameroot)", want: readsRoot},
		{name: "nameext", expr: "$(inputs.f.nameext)", want: readsExt},
		{name: "a measured size", expr: "$(inputs.f.size)", want: int64(1024)},
		{name: "class", expr: "$(inputs.f.class)", want: ClassFile},
		{name: "a bracketed field", expr: `$(inputs.f["basename"])`, want: readsName},
		{name: "a secondary file", expr: "$(inputs.f.secondaryFiles[0].basename)", want: indexName},
		{name: "a directory inside one", expr: "$(inputs.f.secondaryFiles[1].class)", want: ClassDirectory},
		{name: "two levels down", expr: "$(inputs.f.secondaryFiles[1].listing[0].basename)", want: notesName},
		{name: "how many secondary files", expr: "$(inputs.f.secondaryFiles.length)", want: int64(2)},
		{name: "the whole file", expr: "$(inputs.f)", want: testFileObject()},
		{name: "into a filename", expr: "out/$(inputs.f.nameroot).txt", want: "out/reads.txt"},
		{name: "from self", expr: "$(self[0].basename)", want: selfName},
		{name: "how many in self", expr: "$(self.length)", want: int64(1)},
	}

	ctx := &EvalContext{
		Inputs: map[string]any{"f": testFile()},
		Self:   []any{&File{Basename: selfName}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkBothPaths(t, tc.expr, tc.want, ctx)
		})
	}
}

// checkBothPaths evaluates expr with and without the JavaScript engine and
// requires the same answer from each, which is the specification's guarantee
// applied to one expectation rather than to a corpus.
func checkBothPaths(t *testing.T, expr string, want any, ctx *EvalContext) {
	t.Helper()

	for _, evaluator := range []*Evaluator{NewEvaluator(), NewEvaluator(WithJS(nil))} {
		got, err := evaluator.Eval(expr, ctx)
		if err != nil {
			t.Fatalf("Eval(%q) returned error: %v", expr, err)
		}

		if !reflect.DeepEqual(got, want) {
			t.Errorf("Eval(%q) = %#v, want %#v", expr, got, want)
		}
	}
}

// TestEvalAbsentFilesystemFieldIsAnError keeps the existing message quality for
// a field that was never filled in, and keeps the two paths agreeing that it is
// a failure rather than a zero.
func TestEvalAbsentFilesystemFieldIsAnError(t *testing.T) {
	t.Parallel()

	cases := []string{
		"$(inputs.f.checksum)",
		"$(inputs.f.size)",
		"$(inputs.f.contents)",
		"$(inputs.f.location)",
		"$(inputs.f.nosuchfield)",
		"$(inputs.d.listing)",
		"$(inputs.d.size)",
	}

	ctx := &EvalContext{Inputs: map[string]any{
		"f": &File{Basename: readsName},
		"d": &Directory{Basename: auxName},
	}}

	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			for _, evaluator := range []*Evaluator{NewEvaluator(), NewEvaluator(WithJS(nil))} {
				got, err := evaluator.Eval(expr, ctx)
				if !errors.Is(err, ErrExpressionEval) {
					t.Errorf("Eval(%q) = %#v, err = %v, want ErrExpressionEval", expr, got, err)
				}
			}
		})
	}
}

// TestEvalFilesystemFieldNotFoundMessage pins the diagnosis, which is the same
// one a map gets: naming the field is what makes a misspelt property findable.
func TestEvalFilesystemFieldNotFoundMessage(t *testing.T) {
	t.Parallel()

	_, err := NewEvaluator().Eval("$(inputs.f.basenam)", &EvalContext{
		Inputs: map[string]any{"f": &File{Basename: readsName}},
	})
	if err == nil {
		t.Fatal("Eval of a misspelt File field succeeded")
	}

	if want := `inputs.f has no field "basenam"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to contain %q", err, want)
	}
}

func TestTypeNameOfFilesystemValues(t *testing.T) {
	t.Parallel()

	for _, value := range []any{testFile(), &Directory{}, File{}, Directory{}} {
		if got := TypeName(value); got != typeNameObject {
			t.Errorf("TypeName(%T) = %q, want %q", value, got, typeNameObject)
		}
	}

	if got := TypeName(make([]FileOrDirectory, 0)); got != typeNameList {
		t.Errorf("TypeName([]FileOrDirectory) = %q, want %q", got, typeNameList)
	}
}

// TestFilesystemConversionEdgeCases closes the defensive branches: the value
// rather than pointer forms, and a nil pointer sitting inside the union, which
// a malformed job object can produce and which must not panic.
func TestFilesystemConversionEdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		want  any
		name  string
	}{
		{
			name:  "a Directory by value",
			value: Directory{Basename: auxName, Listing: make([]FileOrDirectory, 0)},
			want:  map[string]any{keyClass: ClassDirectory, keyBasename: auxName, keyListing: make([]any, 0)},
		},
		{name: "a nil File pointer", value: (*File)(nil), want: nil},
		{name: "a nil Directory pointer", value: (*Directory)(nil), want: nil},
		{
			name:  "a nil Directory inside a union slice",
			value: []FileOrDirectory{(*Directory)(nil)},
			want:  []any{nil},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ToExpressionValue(tc.value); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ToExpressionValue(%#v) = %#v, want %#v", tc.value, got, tc.want)
			}
		})
	}

	// A nil pointer is not an object, so it must not be viewed as one.
	for _, value := range []any{(*File)(nil), (*Directory)(nil)} {
		if got := TypeName(value); got == typeNameObject {
			t.Errorf("TypeName(%T nil) = %q, want it not to claim an object", value, got)
		}
	}
}

// TestFromExpressionValueIntegerShapes covers the number types a size can
// arrive as, depending on whether a document, a hand-built job object or a
// JavaScript expression produced it.
func TestFromExpressionValueIntegerShapes(t *testing.T) {
	t.Parallel()

	for _, size := range []any{5, int32(5), int64(5), 5.0} {
		got, err := FromExpressionValue(map[string]any{keyClass: ClassFile, keySize: size})
		if err != nil {
			t.Fatalf("FromExpressionValue with a %T size returned error: %v", size, err)
		}

		file, ok := got.(*File)
		if !ok {
			t.Fatalf("FromExpressionValue = %#v, want a *File", got)
		}

		if !file.Size.IsSet() || file.Size.Int() != 5 {
			t.Errorf("a %T size read back as %s, want 5", size, file.Size)
		}
	}

	// Past 2^53 a JavaScript number is no longer an exact integer, so reading
	// it back as one would invent precision the expression never had.
	_, err := FromExpressionValue(map[string]any{keyClass: ClassFile, keySize: 1e18})
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("an inexact size gave %v, want ErrExpressionEval", err)
	}
}

// TestFilesystemObjectOfANilInterfaceIsNil covers filesystemObject's type-switch
// default, reached only when the FileOrDirectory interface value itself is nil
// rather than holding a nil *File or *Directory.
func TestFilesystemObjectOfANilInterfaceIsNil(t *testing.T) {
	t.Parallel()

	if got := filesystemObject(nil); got != nil {
		t.Errorf("filesystemObject(nil) = %#v, want nil", got)
	}
}
