package cwlexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// ojMadeName is the file the cwl.output.json fixtures claim to have produced.
const ojMadeName = "made.txt"

// ojTool is a tool declaring one File output whose glob would never match, so that any value in the
// result must have come from the file rather than from binding.
func ojTool() *cwlcore.CommandLineTool {
	return execTool(nil, outTestParam(execOutID, outTypeOptionalFile, outGlobBinding("never-matches-*")))
}

// ojLoad writes a cwl.output.json into outdir, alongside the file it claims to have produced, and
// loads it.
func ojLoad(t *testing.T, outdir, body string) (map[string]any, error) {
	t.Helper()

	outWriteFile(t, outdir, OutputJSONFile, body)
	outWriteFile(t, outdir, ojMadeName, execGreeting)

	return LoadOutputJSON(ojTool(), outdir, nil)
}

func TestLoadOutputJSONIsAbsentByDefault(t *testing.T) {
	t.Parallel()

	_, err := LoadOutputJSON(ojTool(), t.TempDir(), nil)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadOutputJSON: error %v does not wrap fs.ErrNotExist", err)
	}
}

func TestLoadOutputJSONNormalizesAFile(t *testing.T) {
	t.Parallel()

	outdir := t.TempDir()

	outputs, err := ojLoad(t, outdir, `{"out": {"class": "File", "path": "made.txt"}, "extra": 1}`)
	if err != nil {
		t.Fatalf("LoadOutputJSON: %v", err)
	}

	file, ok := outputs[outPort].(*cwlcore.File)
	if !ok {
		t.Fatalf("output %q = %#v, want a File", outPort, outputs[outPort])
	}

	if file.Path != filepath.Join(outdir, ojMadeName) {
		t.Errorf("path = %q, want it resolved against the output directory", file.Path)
	}

	if file.Checksum != outSumHello || !file.Size.IsSet() {
		t.Errorf("checksum = %q, size = %s; want both measured from disk", file.Checksum, file.Size)
	}

	// A field naming no declared port has nowhere to travel and is dropped rather than smuggled
	// through.
	if _, present := outputs["extra"]; present {
		t.Error("an undeclared field reached the output object")
	}
}

func TestLoadOutputJSONFillsAMissingPortWithNull(t *testing.T) {
	t.Parallel()

	outputs, err := ojLoad(t, t.TempDir(), `{}`)
	if err != nil {
		t.Fatalf("LoadOutputJSON: %v", err)
	}

	value, present := outputs[outPort]
	if !present || value != nil {
		t.Errorf("output %q = %#v, present = %v; want a present null", outPort, value, present)
	}
}

func TestLoadOutputJSONKeepsIntegersIntegral(t *testing.T) {
	t.Parallel()

	tool := execTool(nil, outTestParam(execOutID, outTypeString, nil))
	outdir := t.TempDir()

	outWriteFile(t, outdir, OutputJSONFile, `{"out": [3, 1.5, 1e999]}`)

	outputs, err := LoadOutputJSON(tool, outdir, nil)
	if err != nil {
		t.Fatalf("LoadOutputJSON: %v", err)
	}

	values, ok := outputs[outPort].([]any)
	if !ok || len(values) != 3 {
		t.Fatalf("output = %#v, want a three-element list", outputs[outPort])
	}

	if values[0] != int64(3) {
		t.Errorf("values[0] = %#v, want int64(3): an integer must not become a float", values[0])
	}

	if values[1] != 1.5 {
		t.Errorf("values[1] = %#v, want 1.5", values[1])
	}

	if values[2] != json.Number("1e999").String() {
		t.Errorf("values[2] = %#v, want the unrepresentable number kept as text", values[2])
	}
}

func TestLoadOutputJSONAcceptsANestedRecord(t *testing.T) {
	t.Parallel()

	outdir := t.TempDir()

	outputs, err := ojLoad(t, outdir, `{"out": {"f": {"class": "File", "path": "made.txt"}, "n": 1}}`)
	if err != nil {
		t.Fatalf("LoadOutputJSON: %v", err)
	}

	record, ok := outputs[outPort].(map[string]any)
	if !ok {
		t.Fatalf("output %q = %#v, want a record", outPort, outputs[outPort])
	}

	pmWantPath(t, record["f"], filepath.Join(outdir, ojMadeName))
}

func TestLoadOutputJSONCompletesADirectoryListing(t *testing.T) {
	t.Parallel()

	outdir := t.TempDir()
	outWriteFile(t, outdir, outNameTree+"/"+outNameA, "alpha")
	outWriteFile(t, outdir, OutputJSONFile, `{"out": {"class": "Directory", "path": "tree"}}`)

	tool := execTool(nil, outTestParam(execOutID, outTypeDirectory, nil))

	outputs, err := LoadOutputJSON(tool, outdir, nil)
	if err != nil {
		t.Fatalf("LoadOutputJSON: %v", err)
	}

	dir, ok := outputs[outPort].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("output %q = %#v, want a Directory", outPort, outputs[outPort])
	}

	// A Directory leaving a tool without its listing is a Directory whose contents nobody can
	// recover, whichever of the two ways the tool reported it.
	assertDeepEqual(t, "listing", outEntryNames(t, dir.Listing), []string{outNameA})
}

func TestLoadOutputJSONFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "not JSON at all", body: "not json"},
		{name: "not an object", body: `[1, 2]`},
		{name: "a path outside the output directory", body: `{"out": {"class": "File", "path": "/etc/passwd"}}`},
		{
			name: "a secondary file outside the output directory",
			body: `{"out": {"class": "File", "path": "made.txt",
			         "secondaryFiles": [{"class": "File", "path": "/etc/passwd"}]}}`,
		},
		{
			name: "a listing entry outside the output directory",
			body: `{"out": {"class": "Directory", "path": ".",
			         "listing": [{"class": "File", "path": "/etc/passwd"}]}}`,
		},
		{
			name: "a basename nothing can be given",
			body: `{"out": {"class": "File", "path": "missing.txt", "basename": "renamed.txt"}}`,
		},
		{name: "a list holding an escaping path", body: `{"out": [{"class": "File", "path": "/etc/passwd"}]}`},
		{name: "a record holding an escaping path", body: `{"out": {"f": {"class": "File", "path": "/etc/passwd"}}}`},
		{
			name: "a secondaryFiles entry that is not a filesystem object",
			body: `{"out": {"class": "File", "path": "made.txt", "secondaryFiles": [1]}}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := ojLoad(t, t.TempDir(), testCase.body)
			if !errors.Is(err, ErrOutputJSON) {
				t.Errorf("LoadOutputJSON: error %v does not wrap %v", err, ErrOutputJSON)
			}
		})
	}
}

func TestLoadOutputJSONAcceptsAPathAnInputOccupies(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	input := outWriteFile(t, source, "whale.txt", outContentHello)

	outdir := t.TempDir()
	outWriteFile(t, outdir, OutputJSONFile, fmt.Sprintf(`{"out": {"class": "File", "path": %q}}`, input))

	// paramref_arguments_roundtrip: a tool handed a File and asked to echo it back names the input
	// where it really lives. That is outside the output directory and is not an escape, which is
	// why the check draws on the invocation's inputs rather than on the directory alone.
	outputs, err := LoadOutputJSON(ojTool(), outdir,
		map[string]any{"f": &cwlcore.File{Path: input, Basename: "whale.txt"}})
	if err != nil {
		t.Fatalf("LoadOutputJSON: %v", err)
	}

	pmWantPath(t, outputs[outPort], input)
}
