package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// render is outputObject encoded, for assertions about the wire shape.
func render(t *testing.T, outputs map[string]any) string {
	t.Helper()

	var buf bytes.Buffer

	err := writeOutputs(&buf, outputs)
	if err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}

	return buf.String()
}

// decode is render parsed back, for assertions about values.
func decode(t *testing.T, outputs map[string]any) map[string]any {
	t.Helper()

	var object map[string]any

	err := json.Unmarshal([]byte(render(t, outputs)), &object)
	if err != nil {
		t.Fatalf("the rendered output object does not parse: %v", err)
	}

	return object
}

// fileOf pulls one File or Directory object out of a decoded output object.
func fileOf(t *testing.T, object map[string]any, name string) map[string]any {
	t.Helper()

	value, ok := object[name].(map[string]any)
	if !ok {
		t.Fatalf("output %q = %v, want an object", name, object[name])
	}

	return value
}

func TestWriteOutputsRendersTheCWLWireShape(t *testing.T) {
	t.Parallel()

	file := &cwlcore.File{
		Location: "file:///out/a.txt",
		Path:     "/out/a.txt",
		Basename: "a.txt",
		Checksum: "sha1$da39a3ee5e6b4b0d3255bfef95601890afd80709",
		Size:     cwlcore.NewOptInt(13),
	}

	got := fileOf(t, decode(t, map[string]any{"f": file}), "f")

	want := map[string]any{
		"class":    classFile,
		"location": "file:///out/a.txt",
		"path":     "/out/a.txt",
		"basename": "a.txt",
		"checksum": "sha1$da39a3ee5e6b4b0d3255bfef95601890afd80709",
		"size":     float64(13),
	}

	for key, value := range want {
		if got[key] != value {
			t.Errorf("File[%q] = %v, want %v", key, got[key], value)
		}
	}
}

func TestWriteOutputsKeepsAbsentApartFromZero(t *testing.T) {
	t.Parallel()

	object := decode(t, map[string]any{
		"unmeasured": &cwlcore.File{Basename: "unmeasured"},
		"empty":      &cwlcore.File{Basename: "empty", Size: cwlcore.NewOptInt(0)},
		"unread":     &cwlcore.Directory{Basename: "unread"},
		"nothing":    &cwlcore.Directory{Basename: "nothing", Listing: make([]cwlcore.FileOrDirectory, 0)},
	})

	// An unmeasured size must not surface as 0 bytes, and an explicit 0
	// must survive: cwltest re-derives both from the file on disk.
	if _, present := fileOf(t, object, "unmeasured")["size"]; present {
		t.Error("an unmeasured File carries a size; absent must not be rendered as zero")
	}

	if got := fileOf(t, object, "empty")["size"]; got != float64(0) {
		t.Errorf("an empty File's size = %v, want 0", got)
	}

	// A nil listing means the runner has not read the directory, which is
	// not the same thing as a directory that is empty.
	if _, present := fileOf(t, object, "unread")["listing"]; present {
		t.Error("an unread Directory carries a listing; it must be omitted")
	}

	listing, present := fileOf(t, object, "nothing")["listing"]
	if !present {
		t.Fatal("an empty Directory has no listing; an empty one must be rendered")
	}

	if items, ok := listing.([]any); !ok || len(items) != 0 {
		t.Errorf("listing = %v, want an empty array", listing)
	}
}

func TestWriteOutputsConvertsFilesAtEveryDepth(t *testing.T) {
	t.Parallel()

	nested := &cwlcore.File{
		Basename:       "primary.bam",
		SecondaryFiles: []cwlcore.FileOrDirectory{&cwlcore.File{Basename: "primary.bam.bai"}},
	}

	object := decode(t, map[string]any{"list": []any{nested}})

	items, ok := object["list"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("list = %v, want one item", object["list"])
	}

	entry, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("list[0] = %v, want a File object", items[0])
	}

	secondary, ok := entry["secondaryFiles"].([]any)
	if !ok || len(secondary) != 1 {
		t.Fatalf("secondaryFiles = %v, want one entry", entry["secondaryFiles"])
	}

	if inner, ok := secondary[0].(map[string]any); !ok || inner["class"] != classFile {
		t.Errorf("secondaryFiles[0] = %v, want a File object", secondary[0])
	}
}

func TestWriteOutputsOrdersKeysDeterministically(t *testing.T) {
	t.Parallel()

	outputs := map[string]any{"zeta": 1, "alpha": 2, "mu": 3, "beta": 4}

	first := render(t, outputs)
	if first != render(t, outputs) {
		t.Error("two renderings of the same object differ")
	}

	positions := make([]int, 0, len(outputs))
	for _, name := range []string{"alpha", "beta", "mu", "zeta"} {
		positions = append(positions, strings.Index(first, `"`+name+`"`))
	}

	for i := 1; i < len(positions); i++ {
		if positions[i-1] >= positions[i] {
			t.Fatalf("keys are not sorted:\n%s", first)
		}
	}
}

func TestWriteOutputsRendersNullAndAnEmptyObject(t *testing.T) {
	t.Parallel()

	object := decode(t, map[string]any{"skipped": nil})
	if value, present := object["skipped"]; !present || value != nil {
		t.Errorf("skipped = %v (present %v), want an explicit null", value, present)
	}

	if got := strings.TrimSpace(render(t, make(map[string]any))); got != "{}" {
		t.Errorf("an empty output object rendered as %q, want {}", got)
	}
}

func TestWriteOutputsReportsAValueItCannotEncode(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writeOutputs(&buf, map[string]any{"bad": make(chan int)})
	if err == nil {
		t.Fatal("writeOutputs succeeded on an unencodable value")
	}

	if buf.Len() != 0 {
		t.Errorf("stdout received %q; a failed rendering must write nothing", buf.String())
	}
}
