package cwlexec

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// jobValueTool builds a tool whose single input "v" has the given type.
// jobPlainText is the ordinary string an Any-typed walk must carry through untouched.
const jobPlainText = "plain"

func jobValueTool(typ cwlcore.TypeRef) *cwlcore.CommandLineTool {
	return jobTool(jobParam("v", typ))
}

func TestScalarTypesAccepted(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		typ  cwlcore.TypeRef
		src  string
		want any
	}{
		cwlcore.PrimitiveString: {typ: jobTypeString, src: "v: " + jobHello, want: jobHello},
		cwlcore.PrimitiveInt:    {typ: jobTypeInt, src: jobSrcSeven, want: int64(7)},
		"long":                  {typ: jobTypeLong, src: jobSrcSeven, want: int64(7)},
		"float":                 {typ: jobTypeFloat, src: jobSrcOneAndAHalf, want: 1.5},
		"double":                {typ: jobTypeDouble, src: jobSrcOneAndAHalf, want: 1.5},
		"int widens":            {typ: jobTypeDouble, src: jobSrcSeven, want: float64(7)},
		"boolean true":          {typ: jobTypeBoolean, src: "v: true", want: true},
		"boolean false":         {typ: jobTypeBoolean, src: "v: false", want: false},
		"null primitive":        {typ: jobTypeNull, src: "v: null", want: nil},
		"nested null": {
			typ:  cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeNull}),
			src:  "v: [null]",
			want: []any{nil},
		},
		"any scalar":        {typ: jobTypeAny, src: jobSrcThree, want: int64(3)},
		"union picks a arm": {typ: jobOptionalOf(jobTypeInt), src: jobSrcThree, want: int64(3)},
		"union picks null":  {typ: jobOptionalOf(jobTypeInt), src: "v: null", want: nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			values := jobMustParse(t, t.TempDir(), tc.src, jobValueTool(tc.typ))
			if !reflect.DeepEqual(values["v"], tc.want) {
				t.Errorf("v = %#v, want %#v", values["v"], tc.want)
			}
		})
	}
}

func TestScalarTypeMismatches(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		typ  cwlcore.TypeRef
		src  string
		want string
	}{
		"string given a number": {typ: jobTypeString, src: jobSrcSeven, want: "expected string, but found int"},
		"int given a float":     {typ: jobTypeInt, src: jobSrcOneAndAHalf, want: "expected int, but found float"},
		"int given a string":    {typ: jobTypeInt, src: "v: seven", want: "expected int, but found string"},
		"float given a string":  {typ: jobTypeFloat, src: "v: x", want: "expected float, but found string"},
		"boolean given a word":  {typ: jobTypeBoolean, src: "v: 'true'", want: "expected boolean, but found string"},
		"scalar given a map":    {typ: jobTypeString, src: "v: {a: 1}", want: "expected string, but found mapping"},
		"null given a value":    {typ: jobTypeNull, src: jobSrcThree, want: "expected null, but found int"},
		"any given null": {
			// Only reachable nested: a top-level null reads as "absent" and is
			// answered by the default rules instead.
			typ:  cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeAny}),
			src:  "v: [null]",
			want: "v[0]: expected Any, but found null",
		},
		"unknown primitive": {typ: cwlcore.NewPrimitiveType("widget"), src: "v: 1", want: `"widget" is not a CWL type`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jobWantMessage(t, jobMustFail(t, t.TempDir(), tc.src, jobValueTool(tc.typ)), tc.want)
		})
	}
}

func TestUnionReportsEveryMember(t *testing.T) {
	t.Parallel()

	typ := cwlcore.NewUnionType([]cwlcore.TypeRef{jobTypeInt, jobTypeBoolean})

	message := jobMustFail(t, t.TempDir(), "v: "+jobHello, jobValueTool(typ))
	jobWantMessage(t, message, "no type in int|boolean accepts this string")
	jobWantMessage(t, message, "expected int, but found string")
	jobWantMessage(t, message, "expected boolean, but found string")
}

func TestArrayValues(t *testing.T) {
	t.Parallel()

	schema := &cwlcore.ArraySchema{Items: jobTypeInt}
	typ := cwlcore.NewArrayType(schema)

	values := jobMustParse(t, t.TempDir(), "v: [1, 2, 3]", jobValueTool(typ))
	if !reflect.DeepEqual(values["v"], []any{int64(1), int64(2), int64(3)}) {
		t.Errorf("v = %#v", values["v"])
	}

	t.Run("a single value is not an array", func(t *testing.T) {
		t.Parallel()

		jobWantMessage(t, jobMustFail(t, t.TempDir(), "v: 1", jobValueTool(typ)), "expected int[], but found int")
	})

	t.Run("element errors name their index", func(t *testing.T) {
		t.Parallel()

		jobWantMessage(t, jobMustFail(t, t.TempDir(), "v: [1, x]", jobValueTool(typ)), "v[1]: expected int")
	})

	t.Run("an array type with no schema", func(t *testing.T) {
		t.Parallel()

		jobWantMessage(t, jobMustFail(t, t.TempDir(), "v: []", jobValueTool(cwlcore.NewArrayType(nil))),
			"array type carries no item schema")
	})
}

func TestArrayOfFilesInheritsLoadContents(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	typ := cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeFile})

	tool := jobTool(cwlcore.CommandInputParameter{
		ParameterBase: cwlcore.ParameterBase{IDField: "v", Type: typ, LoadContents: true},
	})

	values := jobMustParse(t, fixtures, "v: [{class: File, location: files/hello.txt}]", tool)

	items, ok := values["v"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("v = %#v, want one element", values["v"])
	}

	file, ok := items[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("v[0] is %T, want *cwlcore.File", items[0])
	}

	if file.Contents.Value() != jobHelloText {
		t.Errorf("contents = %q, want joLoadContents to apply element by element", file.Contents.Value())
	}
}

func TestRecordValues(t *testing.T) {
	t.Parallel()

	schema := &cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{
			{Name: "file:///t.cwl#rec/count", Type: jobTypeInt},
			{Name: "file:///t.cwl#rec/label", Type: jobOptionalOf(jobTypeString)},
		},
	}
	typ := cwlcore.NewRecordType(schema)

	values := jobMustParse(t, t.TempDir(), "v: {count: 2, label: x}", jobValueTool(typ))

	want := map[string]any{"count": int64(2), "label": "x"}
	if !reflect.DeepEqual(values["v"], want) {
		t.Errorf("v = %#v, want %#v", values["v"], want)
	}

	cases := map[string]struct {
		src  string
		want string
	}{
		"missing required field": {src: "v: {label: x}", want: `field "count" is required`},
		jobCaseUnknownField:      {src: "v: {count: 1, other: 2}", want: `"other" is not a declared field of v`},
		"wrong field type":       {src: "v: {count: x}", want: "v.count: expected int"},
		jobCaseNotAMapping:       {src: "v: 1", want: "expected record, but found int"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jobWantMessage(t, jobMustFail(t, t.TempDir(), tc.src, jobValueTool(typ)), tc.want)
		})
	}

	t.Run("a record type with no schema", func(t *testing.T) {
		t.Parallel()

		jobWantMessage(t, jobMustFail(t, t.TempDir(), "v: {}", jobValueTool(cwlcore.NewRecordType(nil))),
			"record type carries no schema")
	})

	t.Run("a record that declares no fields at all", func(t *testing.T) {
		t.Parallel()

		empty := jobValueTool(cwlcore.NewRecordType(&cwlcore.RecordSchema{}))

		jobWantMessage(t, jobMustFail(t, t.TempDir(), "v: {other: 2}", empty), "expected one of none")
	})
}

func TestEnumValues(t *testing.T) {
	t.Parallel()

	schema := &cwlcore.EnumSchema{Symbols: []string{"file:///t.cwl#e/red", "file:///t.cwl#e/blue"}}
	typ := cwlcore.NewEnumType(schema)

	values := jobMustParse(t, t.TempDir(), "v: blue", jobValueTool(typ))
	if values["v"] != "blue" {
		t.Errorf("v = %v, want %q", values["v"], "blue")
	}

	t.Run("an unknown symbol", func(t *testing.T) {
		t.Parallel()

		message := jobMustFail(t, t.TempDir(), "v: green", jobValueTool(typ))
		jobWantMessage(t, message, `"green" is not one of the enum symbols "red", "blue"`)
	})

	t.Run("a non-string", func(t *testing.T) {
		t.Parallel()

		jobWantMessage(t, jobMustFail(t, t.TempDir(), "v: 1", jobValueTool(typ)), "expected enum, but found int")
	})

	t.Run("an enum type with no schema", func(t *testing.T) {
		t.Parallel()

		jobWantMessage(t, jobMustFail(t, t.TempDir(), "v: red", jobValueTool(cwlcore.NewEnumType(nil))),
			"enum type carries no schema")
	})
}

func TestStdinShortcutIsAFile(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)
	typ := cwlcore.NewShortcutType(cwlcore.TypeKindStdin)

	file := jobFileValue(
		t,
		jobMustParse(t, fixtures, "v: {class: File, location: files/hello.txt}", jobValueTool(typ)),
		"v",
	)
	if file.Checksum != jobSumHello {
		t.Errorf("checksum = %q", file.Checksum)
	}

	t.Run("wrong shape", func(t *testing.T) {
		t.Parallel()

		jobWantMessage(t, jobMustFail(t, fixtures, "v: hello.txt", jobValueTool(typ)),
			"expected a mapping with class: File")
	})
}

// jobAssertWalkedRecord checks that an unchecked mapping kept its plain members and had its
// filesystem members normalised.
func jobAssertWalkedRecord(t *testing.T, values map[string]any) {
	t.Helper()

	record, ok := values["v"].(map[string]any)
	if !ok {
		t.Fatalf("v is %T, want a mapping", values["v"])
	}

	if record["a"] != int64(1) {
		t.Errorf("v.a = %v, want 1", record["a"])
	}

	file, ok := record["f"].(*cwlcore.File)
	if !ok {
		t.Fatalf("v.f is %T, want *cwlcore.File", record["f"])
	}

	if file.Checksum != jobSumHello {
		t.Errorf("v.f checksum = %q", file.Checksum)
	}
}

func TestNamedAndUnsetTypesArePassedThrough(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	for name, typ := range map[string]cwlcore.TypeRef{
		"named": cwlcore.NewNamedType("file:///t.cwl#MyRecord"),
		"unset": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Unresolvable here, so the value is walked but not checked — except that
			// filesystem objects inside it are still normalised.
			src := "v: {a: 1, f: {class: File, location: files/hello.txt}}"

			jobAssertWalkedRecord(t, jobMustParse(t, fixtures, src, jobValueTool(typ)))
		})
	}
}

func TestAnyWalksNestedFilesystemObjects(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	src := "v:\n" +
		"  - {class: File, location: files/hello.txt}\n" +
		"  - {class: Directory, location: dir}\n" +
		"  - " + jobPlainText + "\n"

	values := jobMustParse(t, fixtures, src, jobValueTool(jobTypeAny))

	items, ok := values["v"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("v = %#v, want three elements", values["v"])
	}

	file, ok := items[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("v[0] is %T, want *cwlcore.File", items[0])
	}

	if want := filepath.Join(fixtures, "files", "hello.txt"); file.Path != want {
		t.Errorf("v[0].path = %q, want %q", file.Path, want)
	}

	if _, ok := items[1].(*cwlcore.Directory); !ok {
		t.Errorf("v[1] is %T, want *cwlcore.Directory", items[1])
	}

	if items[2] != jobPlainText {
		t.Errorf("v[2] = %v, want %q", items[2], jobPlainText)
	}
}

func TestAnyPropagatesNestedFailures(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]struct {
		src  string
		want string
	}{
		"inside a sequence": {src: "v: [{class: File}]", want: "v[0]: a File must supply"},
		"inside a mapping":  {src: "v: {inner: {class: File}}", want: "v.inner: a File must supply"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jobWantMessage(t, jobMustFail(t, fixtures, tc.src, jobValueTool(jobTypeAny)), tc.want)
		})
	}
}

func TestClassOfIgnoresMappingsWithoutAClass(t *testing.T) {
	t.Parallel()

	values := jobMustParse(t, t.TempDir(), "v: {a: 1}", jobValueTool(jobTypeAny))

	record, ok := values["v"].(map[string]any)
	if !ok || record["a"] != int64(1) {
		t.Errorf("v = %#v, want a plain mapping", values["v"])
	}
}

func TestDescribeType(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		typ  cwlcore.TypeRef
		want string
	}{
		cwlcore.PrimitiveFile: {typ: jobTypeFile, want: "a mapping with class: " + cwlcore.PrimitiveFile},
		"Directory":           {typ: jobTypeDirectory, want: "a mapping with class: Directory"},
		"stdin": {
			typ:  cwlcore.NewShortcutType(cwlcore.TypeKindStdin),
			want: "a mapping with class: File",
		},
		cwlcore.PrimitiveString: {typ: jobTypeString, want: cwlcore.PrimitiveString},
		"union":                 {typ: jobOptionalOf(jobTypeInt), want: "null|int"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := joDescribeType(tc.typ); got != tc.want {
				t.Errorf("joDescribeType = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCapWriterStopsAtItsLimit(t *testing.T) {
	t.Parallel()

	writer := &joCapWriter{limit: 4}

	for _, chunk := range []string{"ab", "cd", "ef"} {
		n, err := writer.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		if n != len(chunk) {
			t.Errorf("Write reported %d bytes, want %d", n, len(chunk))
		}
	}

	const want = "abcd"

	if string(writer.buf) != want {
		t.Errorf("buf = %q, want %q", writer.buf, want)
	}
}
