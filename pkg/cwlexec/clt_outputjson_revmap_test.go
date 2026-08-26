package cwlexec

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Reading an output object a tool wrote in a namespace that is not this host's.
//
// The two conformance documents that need it are test-cwl-out.cwl and test-cwl-out2.cwl, run under
// DockerRequirement as docker_json_output_path and docker_json_output_location. Both echo
// `$(runtime.outdir)/foo` into cwl.output.json, which inside a container is the container's own
// output directory, and differ only in whether they write it as a `path` or as a file:// `location`.

// ojToolDir and ojHostDir stand in for the two namespaces one contained invocation spans.
const (
	ojToolDir = "/CONTNR"
	ojHostDir = "/var/host/out"
)

// ojRevmap is the mapping those two directories imply, in the shape [WithHostPaths] takes.
func ojRevmap(target string) string {
	rest, inTool := relativeTo(ojToolDir, target)
	if !inTool {
		return target
	}

	return filepath.Join(ojHostDir, rest)
}

func TestRevmapPathsRewritesOnlyWhatNamesAFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		object map[string]any
		want   map[string]any
		name   string
	}{{
		name:   "an absolute path is mapped",
		object: map[string]any{outKeyPath: ojToolDir + "/foo"},
		want:   map[string]any{outKeyPath: ojHostDir + "/foo"},
	}, {
		name:   "a file URI location is mapped, and stays a URI",
		object: map[string]any{outKeyLocation: fileScheme + ojToolDir + "/foo"},
		want:   map[string]any{outKeyLocation: fileScheme + ojHostDir + "/foo"},
	}, {
		name:   "a location written as a bare path is mapped too",
		object: map[string]any{outKeyLocation: ojToolDir + "/foo"},
		want:   map[string]any{outKeyLocation: ojHostDir + "/foo"},
	}, {
		// It resolves against the output directory, which the collector already reads as
		// this host's, so mapping it would apply the translation twice.
		name:   "a relative path is left alone",
		object: map[string]any{outKeyPath: "foo"},
		want:   map[string]any{outKeyPath: "foo"},
	}, {
		name:   "a relative file URI is left alone",
		object: map[string]any{outKeyLocation: fileScheme + "foo"},
		want:   map[string]any{outKeyLocation: fileScheme + "foo"},
	}, {
		// It names something that is not on a filesystem, so there is nothing to map.
		name:   "a location under another scheme is left alone",
		object: map[string]any{outKeyLocation: "https://example.org/foo"},
		want:   map[string]any{outKeyLocation: "https://example.org/foo"},
	}, {
		name:   "a path outside every directory the map spans is left alone",
		object: map[string]any{outKeyPath: outEscapePath},
		want:   map[string]any{outKeyPath: outEscapePath},
	}, {
		name:   "a path that is not a string is left alone",
		object: map[string]any{outKeyPath: int64(3), outKeyLocation: true},
		want:   map[string]any{outKeyPath: int64(3), outKeyLocation: true},
	}, {
		name:   "a field that is not a path is left alone, whatever it holds",
		object: map[string]any{outKeyBasename: ojToolDir + "/foo", outKeySize: int64(4)},
		want:   map[string]any{outKeyBasename: ojToolDir + "/foo", outKeySize: int64(4)},
	}, {
		name: "an array descends, and so does the object under it",
		object: map[string]any{outKeySecondaryFiles: []any{
			map[string]any{outKeyPath: ojToolDir + "/foo.bai"},
		}},
		want: map[string]any{outKeySecondaryFiles: []any{
			map[string]any{outKeyPath: ojHostDir + "/foo.bai"},
		}},
	}, {
		name: "a nested listing descends",
		object: map[string]any{outKeyListing: []any{
			map[string]any{outKeyListing: []any{
				map[string]any{outKeyLocation: fileScheme + ojToolDir + "/deep/x"},
			}},
		}},
		want: map[string]any{outKeyListing: []any{
			map[string]any{outKeyListing: []any{
				map[string]any{outKeyLocation: fileScheme + ojHostDir + "/deep/x"},
			}},
		}},
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := revmapPaths(testCase.object, ojRevmap)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("revmapPaths = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestLoadOutputJSONReadsAContainerWrittenObject is the whole hop: a file naming a path only the
// tool has, loaded into an output object naming one this host does, with the size and the checksum
// measured from the bytes at the end of it.
func TestLoadOutputJSONReadsAContainerWrittenObject(t *testing.T) {
	t.Parallel()

	// The two spellings the two conformance documents use, over the same file.
	spellings := map[string]string{
		outKeyPath:     ojToolDir + "/" + ojMadeName,
		outKeyLocation: fileScheme + ojToolDir + "/" + ojMadeName,
	}

	for key, written := range spellings {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			ojWantRevmapped(t, key, written)
		})
	}
}

// ojWantRevmapped loads one spelling of the container-written object and asserts it came back
// naming this host's copy, measured.
func ojWantRevmapped(t *testing.T, key, written string) {
	t.Helper()

	dir := t.TempDir()
	outWriteFile(t, dir, ojMadeName, execGreeting)
	outWriteFile(t, dir, OutputJSONFile,
		fmt.Sprintf(`{"out": {"class": "File", %q: %q}}`, key, written))

	revmap := func(target string) string {
		rest, inTool := relativeTo(ojToolDir, target)
		if !inTool {
			return target
		}

		return filepath.Join(dir, rest)
	}

	outputs, err := LoadOutputJSON(ojTool(), dir, nil, WithHostPaths(revmap))
	if err != nil {
		t.Fatalf("LoadOutputJSON: %v", err)
	}

	file, ok := outputs["out"].(*cwlcore.File)
	if !ok {
		t.Fatalf("out = %#v, want a *cwlcore.File", outputs["out"])
	}

	if file.Path != filepath.Join(dir, ojMadeName) {
		t.Errorf("path = %q, want the file on this host", file.Path)
	}

	if file.Size.Int() != int64(len(execGreeting)) {
		t.Errorf("size = %s, want %d measured from disk", file.Size, len(execGreeting))
	}
}

// TestHostOutputPathLeavesUnspannedPathsAlone pins the difference from [PathMap.hostPath]: a path
// the tool chose rather than one this map planned has no .outside placement waiting for it.
func TestHostOutputPathLeavesUnspannedPathsAlone(t *testing.T) {
	t.Parallel()

	contained := pmcMap()

	cases := map[string]string{
		pmcToolWork + "/foo":  pmWork + "/foo",
		pmcToolStage + "/foo": pmStage + "/foo",
		outEscapePath:         outEscapePath,
	}

	for target, want := range cases {
		if got := contained.hostOutputPath(target); got != want {
			t.Errorf("hostOutputPath(%q) = %q, want %q", target, got, want)
		}
	}

	// Without a container the tool wrote its output object in this host's namespace already.
	host := pmMap()
	if got := host.hostOutputPath(pmWork + "/foo"); got != pmWork+"/foo" {
		t.Errorf("hostOutputPath without a container = %q, want the identity", got)
	}
}
