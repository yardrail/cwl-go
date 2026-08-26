package cwlcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// toolDocument is the smallest document that loads, validates and decodes.
const toolDocument = `cwlVersion: v1.2
class: CommandLineTool
baseCommand: echo
inputs: []
outputs: []
`

// writeDocument writes src to a fresh temporary file and returns its path.
func writeDocument(t *testing.T, name, src string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)

	err := os.WriteFile(path, []byte(src), 0o600)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return path
}

func TestFetchReadsALocalPath(t *testing.T) {
	t.Parallel()

	path := writeDocument(t, "doc.cwl", toolDocument)

	src, url, err := Fetch(path)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if string(src) != toolDocument {
		t.Errorf("Fetch returned %q", src)
	}

	if !strings.HasPrefix(url, "file://") || !strings.HasSuffix(url, "/doc.cwl") {
		t.Errorf("Fetch returned url %q, want an absolute file URL", url)
	}
}

func TestFetchReportsAMissingFile(t *testing.T) {
	t.Parallel()

	_, _, err := Fetch(filepath.Join(t.TempDir(), "absent.cwl"))
	if err == nil {
		t.Fatal("Fetch of a missing file returned no error")
	}

	if !strings.Contains(Explain(err), "absent.cwl") {
		t.Errorf("Explain = %q, want the missing file named", Explain(err))
	}
}
