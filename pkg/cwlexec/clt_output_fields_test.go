package cwlexec

import (
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The rendering boundary: what a typed filesystem value looks like once an expression can read it.
//
// [cwlcore.ToExpressionValue] owns the rule; this package owns the field names it is read back
// with, and the two call sites that produce a File. What is asserted here is that those three
// agree.

// outNoExtName is a basename with no extension at all.
const outNoExtName = "README"

// outObjectKeys lists an expression object's field names in sorted order.
func outObjectKeys(t *testing.T, value any) []string {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want an object", value)
	}

	names := make([]string, 0, len(object))
	for key := range object {
		names = append(names, key)
	}

	slices.Sort(names)

	return names
}

// outFileKeys is what a File with nothing but a basename renders as, and the property the whole of
// this file's rendering rests on: the three name fields travel together, so a name with no
// extension still carries an empty nameext.
var outFileKeys = []string{outKeyBasename, outKeyClass, outKeyNameext, outKeyNameroot}

func TestExpressionObjectRendersEveryField(t *testing.T) {
	t.Parallel()

	file := &cwlcore.File{Basename: outNameA, Nameroot: "a", Nameext: jobExtTxt}
	dir := &cwlcore.Directory{Basename: "d"}

	rendered := outExpressionObject(map[string]any{
		"f":           file,
		"d":           dir,
		"list":        []cwlcore.FileOrDirectory{file, dir},
		stepNested:    []any{map[string]any{"inner": file}},
		"plain":       int64(3),
		jobCaseAbsent: nil,
	})

	assertDeepEqual(t, "File", outObjectKeys(t, rendered["f"]), outFileKeys)
	assertDeepEqual(t, "Directory", outObjectKeys(t, rendered["d"]),
		[]string{outKeyBasename, outKeyClass})

	list, ok := rendered["list"].([]any)
	if !ok {
		t.Fatalf("list = %#v, want a list", rendered["list"])
	}

	assertInt(t, "len(list)", len(list), 2)

	nested, ok := rendered[stepNested].([]any)
	if !ok {
		t.Fatalf("nested = %#v, want a list", rendered[stepNested])
	}

	wrapper, ok := nested[0].(map[string]any)
	if !ok {
		t.Fatalf("nested[0] = %#v, want an object", nested[0])
	}

	assertDeepEqual(t, "nested File", outObjectKeys(t, wrapper["inner"]), outFileKeys)

	// Anything that is not a filesystem value, a list or an object passes through untouched.
	assertDeepEqual(t, "scalar", rendered["plain"], int64(3))
	assertDeepEqual(t, "absent", rendered[jobCaseAbsent], nil)
}

// TestWidenKeepsValuesTyped pins the difference between widening and rendering: outWiden hands the
// values themselves to a path rewriter or an output port, which is why it is not
// cwlcore.ToExpressionValue.
func TestWidenKeepsValuesTyped(t *testing.T) {
	t.Parallel()

	file := &cwlcore.File{Basename: outNameA}

	widened := outWiden([]cwlcore.FileOrDirectory{file})
	assertInt(t, "len", len(widened), 1)

	if widened[0] != any(file) {
		t.Errorf("outWiden[0] = %#v, want the *cwlcore.File itself", widened[0])
	}
}

// TestFileFieldNamesMatchCwlcore is the drift alarm for the one set of names this package and
// pkg/cwlcore each hold a copy of.
//
// cwlcore's copy is unexported by design — it cannot import this package, and exporting thirteen
// constants to reach literal single-sourcing would widen its API for no behavioural gain — so the
// two are compared where it actually matters: the object cwlcore renders must be keyed by exactly
// the names this package reads it back with. A rename on either side fails here.
func TestFileFieldNamesMatchCwlcore(t *testing.T) {
	t.Parallel()

	file := &cwlcore.File{
		Location: "file:///d/a.txt",
		Path:     "/d/a.txt",
		Basename: outNameA,
		Dirname:  "/d",
		Nameroot: "a",
		Nameext:  jobExtTxt,
		Checksum: "sha1$00",
		Format:   "http://example.org/fmt",
		Size:     cwlcore.NewOptInt(1),
		Contents: cwlcore.NewOptString("x"),
		SecondaryFiles: []cwlcore.FileOrDirectory{
			&cwlcore.Directory{Basename: "d", Listing: make([]cwlcore.FileOrDirectory, 0)},
		},
	}

	assertDeepEqual(t, "File", outObjectKeys(t, cwlcore.ToExpressionValue(file)), []string{
		outKeyBasename, outKeyChecksum, outKeyClass, outKeyContents, outKeyDirname, outKeyFormat,
		outKeyLocation, outKeyNameext, outKeyNameroot, outKeyPath, outKeySecondaryFiles, outKeySize,
	})

	dir := &cwlcore.Directory{
		Location: "file:///d",
		Path:     "/d",
		Basename: "d",
		Listing:  make([]cwlcore.FileOrDirectory, 0),
	}

	// Directory's set is much smaller, and deliberately so: the vendored schema gives it only
	// class, location, path, basename and listing.
	assertDeepEqual(t, "Directory", outObjectKeys(t, cwlcore.ToExpressionValue(dir)),
		[]string{outKeyBasename, outKeyClass, outKeyListing, outKeyLocation, outKeyPath})
}

// TestFileObjectKeepsZeroValuedFields is the absent-versus-zero guarantee read from this side of
// the boundary: a size of 0 and contents of "" describe an empty file, and dropping them because
// they are the Go zero value would tell an expression the file was never measured or never read.
func TestFileObjectKeepsZeroValuedFields(t *testing.T) {
	t.Parallel()

	empty := outExpressionObject(map[string]any{"f": &cwlcore.File{
		Basename: jobNameEmpty + jobExtTxt,
		Nameroot: jobNameEmpty,
		Nameext:  jobExtTxt,
		Size:     cwlcore.NewOptInt(0),
		Contents: cwlcore.NewOptString(""),
	}})["f"]

	object, ok := empty.(map[string]any)
	if !ok {
		t.Fatalf("rendered = %#v, want an object", empty)
	}

	assertDeepEqual(t, "size", object[outKeySize], int64(0))
	assertDeepEqual(t, "contents", object[outKeyContents], "")

	if _, present := object[outKeySecondaryFiles]; present {
		t.Error("a nil secondaryFiles must be absent, not an empty array")
	}
}

// TestFileObjectNameFieldsAgree is the README case, asserted from both of the call sites that
// produce a File: a job order read off disk, and an output collected from one.
//
// Both must render nameext as present and empty. Process.yml requires
// `nameroot + nameext == basename`, which omission makes unsatisfiable, and a parameter reference
// such as `$(self.nameroot).idx6$(self.nameext)` — tests/search.cwl does exactly this — fails on a
// missing key rather than substituting nothing. It is also what the reference implementation
// produces: cwltool's normalizeFilesDirs assigns both halves of os.path.splitext(basename)
// unconditionally, and splitext("README") is ("README", "").
func TestFileObjectNameFieldsAgree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNoExtName, "x")

	values := jobMustParse(t, dir,
		"f: {class: File, path: "+outNoExtName+"}\n", jobTool(jobParam("f", jobTypeFile)))

	outputs := outCollect(t,
		outTestTool(outTestParam("o", outTypeFile, outGlobBinding(outNoExtName))), dir, 0)

	collectedFile, ok := outputs["o"].(*cwlcore.File)
	if !ok {
		t.Fatalf("collected output = %#v, want a *cwlcore.File", outputs["o"])
	}

	loaded := outNameFields(t, jobFileValue(t, values, "f"))
	collected := outNameFields(t, collectedFile)

	want := map[string]any{
		outKeyBasename: outNoExtName,
		outKeyNameroot: outNoExtName,
		outKeyNameext:  "",
	}

	assertDeepEqual(t, "job order", loaded, want)
	assertDeepEqual(t, "collected", collected, want)
}

// outNameFields renders one File and picks out the three name fields.
func outNameFields(t *testing.T, file *cwlcore.File) map[string]any {
	t.Helper()

	object, ok := cwlcore.ToExpressionValue(file).(map[string]any)
	if !ok {
		t.Fatalf("ToExpressionValue(%#v) did not render an object", file)
	}

	names := []string{outKeyBasename, outKeyNameroot, outKeyNameext}
	fields := make(map[string]any, len(names))

	for _, key := range names {
		value, present := object[key]
		if !present {
			t.Errorf("%s is absent from %#v", key, object)
		}

		fields[key] = value
	}

	return fields
}

func TestTextField(t *testing.T) {
	t.Parallel()

	object := map[string]any{outKeyKept: "value", "number": int64(3)}

	assertDeepEqual(t, "kept", outTextField(object, outKeyKept), "value")
	assertDeepEqual(t, "absent", outTextField(object, "dropped"), "")
	assertDeepEqual(t, "non-string", outTextField(object, "number"), "")
}
