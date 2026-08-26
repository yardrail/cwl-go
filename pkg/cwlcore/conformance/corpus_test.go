package conformance

import (
	"path/filepath"
	"testing"
)

// testDoc is a stand-in corpus document path.
const testDoc = "tests/a.cwl"

func TestSafeJoinRefusesEscapes(t *testing.T) {
	t.Parallel()

	root := filepath.FromSlash("/dest")

	tests := []struct {
		name string
		rel  string
		ok   bool
	}{
		{name: "a normal member", rel: testDoc, ok: true},
		{name: "the wrapper entry itself", rel: "", ok: true},
		{name: "a parent traversal", rel: "../evil", ok: false},
		{name: "a deep traversal", rel: "tests/../../evil", ok: false},
		{name: "an absolute path", rel: "/etc/passwd", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, ok := safeJoin(root, tt.rel)
			if ok != tt.ok {
				t.Errorf("safeJoin(%q, %q) ok = %v, want %v", root, tt.rel, ok, tt.ok)
			}
		})
	}
}

func TestStripLeadingComponent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "the GitHub wrapper is dropped", in: "cwl-v1.2-1.2.1/" + testDoc, want: testDoc},
		{name: "the wrapper directory itself", in: "cwl-v1.2-1.2.1/", want: ""},
		{name: "a bare name has nothing to strip", in: "README", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := stripLeadingComponent(tt.in)
			if got != tt.want {
				t.Errorf("stripLeadingComponent(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLocalPathAcceptsBothSpellings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "a file URL", ref: "file:///a/b/c.yaml", want: filepath.FromSlash("/a/b/c.yaml")},
		{name: "a bare path", ref: "/a/b/c.yaml", want: filepath.FromSlash("/a/b/c.yaml")},
		{name: "nothing", ref: "", want: ""},
		{name: "a scheme we cannot map", ref: "https://example.com/a.yaml", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := localPath(tt.ref)
			if got != tt.want {
				t.Errorf("localPath(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestDefaultCacheDirHonoursTheOverride(t *testing.T) {
	t.Setenv(envCache, filepath.FromSlash("/somewhere/else"))

	got := defaultCacheDir()
	if got != filepath.FromSlash("/somewhere/else") {
		t.Errorf("defaultCacheDir() = %q, want the override", got)
	}
}

func TestDedupeSortsAndDeduplicates(t *testing.T) {
	t.Parallel()

	got := dedupe([]string{"b", "a", "b", "c", "a"})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("dedupe = %v, want [a b c]", got)
	}
}

func TestWorkerCountNeverExceedsTheWork(t *testing.T) {
	t.Parallel()

	if got := workerCount(1); got != 1 {
		t.Errorf("workerCount(1) = %d, want 1", got)
	}

	if got := workerCount(0); got != 1 {
		t.Errorf("workerCount(0) = %d, want 1", got)
	}
}
