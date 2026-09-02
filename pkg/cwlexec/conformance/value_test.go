package conformance

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeRejectsAValueWithNoJSONSpelling(t *testing.T) {
	t.Parallel()

	// The renderer writes a NaN as json.dumps does, which keeps an interpolated
	// string readable and is not parseable JSON. cwl-run refuses to write such an
	// object at all, so a run that produced one fails there; here it surfaces as a
	// value the comparison cannot read, which is the same verdict.
	_, err := normalize(map[string]any{outputName: math.NaN()})
	if err == nil {
		t.Error("a NaN was accepted as a comparable value")
	}
}

func TestNormalizeKeepsIntegersExact(t *testing.T) {
	t.Parallel()

	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(42), nil)

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "an int64", value: int64(42), want: "42"},
		{name: "a fraction", value: 0.5, want: "0.5"},
		{name: "a whole float", value: 1.0, want: "1.0"},
		{name: "a magnitude past int64", value: 1e42, want: huge.String()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalize(tt.value)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}

			number, ok := got.(json.Number)
			if !ok {
				t.Fatalf("normalize returned %T, want a json.Number", got)
			}

			if number.String() != tt.want {
				t.Errorf("normalize = %s, want %s", number, tt.want)
			}
		})
	}
}

func TestEqualScalarComparesByValueNotRepresentation(t *testing.T) {
	t.Parallel()

	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(42), nil)
	offByOne := new(big.Int).Add(huge, big.NewInt(1))

	tests := []struct {
		name string
		a    any
		b    any
		want bool
	}{
		{name: "the same integer written two ways", a: json.Number("1"), b: json.Number("1.0"), want: true},
		{
			name: "a huge integer and the float nearest it",
			a:    json.Number(huge.String()),
			b:    json.Number("1e42"),
			want: false,
		},
		{name: "a huge integer and itself", a: json.Number(huge.String()), b: json.Number(huge.String()), want: true},
		{
			name: "two huge integers one apart",
			a:    json.Number(huge.String()),
			b:    json.Number(offByOne.String()),
			want: false,
		},
		{name: "a number and a map", a: json.Number("1"), b: make(map[string]any), want: false},
		// Neither side is a number, so this reaches comparableEqual with a plain
		// string on the left -- the only way to exercise the guard against b's
		// dynamic type without a's own guard short-circuiting first.
		{name: "a string and a map", a: "x", b: make(map[string]any), want: false},
		{name: "a string and a slice", a: "x", b: make([]any, 0), want: false},
		{name: "two maps", a: make(map[string]any), b: make(map[string]any), want: false},
		{name: "two slices", a: make([]any, 0), b: make([]any, 0), want: false},
		{name: "two nulls", a: nil, b: nil, want: true},
		{name: "an unparseable literal", a: json.Number("nonsense"), b: json.Number("1"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := equalScalar(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("equalScalar(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestEqualIntegerAndFloatRejectsInfinity exercises the exact == nil branch:
// [strconv.ParseFloat]("Inf", 64) succeeds, but [big.Rat.SetFloat64] answers nil for an
// infinity, which is neither equal nor unequal to any integer in the ordinary sense --
// just not equal here.
func TestEqualIntegerAndFloatRejectsInfinity(t *testing.T) {
	t.Parallel()

	// "Inf" carries no digits, so integerLiteral rejects it and equalNumber routes the
	// comparison through equalIntegerAndFloat.
	got := equalNumber(json.Number("1"), json.Number("Inf"))
	if got {
		t.Error("an integer was accepted as equal to an infinity")
	}

	got = equalIntegerAndFloat(big.NewInt(1), json.Number("Inf"))
	if got {
		t.Error("equalIntegerAndFloat accepted an infinity")
	}
}

func TestCompareContentsReadsTheFileBack(t *testing.T) {
	t.Parallel()

	path := writeFile(t, fixtureText)

	tests := []struct {
		name     string
		contents any
		reported map[string]any
		match    bool
	}{
		{name: "the bytes on disk", contents: fixtureText, reported: fileOutput(path), match: true},
		{name: "other bytes", contents: "goodbye\n", reported: fileOutput(path), match: false},
		{
			name: "a file that is not there", contents: fixtureText,
			reported: fileOutput(filepath.Join(t.TempDir(), fixtureName)), match: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expected := map[string]any{keyClass: classFile, keyContents: tt.contents}

			err := check(t, expected, tt.reported)
			if (err == nil) != tt.match {
				t.Errorf("match = %v, want %v (%v)", err == nil, tt.match, err)
			}
		})
	}
}

func TestLocalPathAcceptsFileURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "a bare path", ref: "/tmp/out.txt", want: filepath.FromSlash("/tmp/out.txt")},
		{name: "a file URL", ref: "file:///tmp/out.txt", want: filepath.FromSlash("/tmp/out.txt")},
		{name: "an http URL", ref: "http://example.invalid/out.txt", want: "http://example.invalid/out.txt"},
		{name: "something that is not a URL at all", ref: "://", want: "://"},
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

func TestFindCorpusReportsWhatIsMissing(t *testing.T) {
	dir := t.TempDir()

	t.Setenv(envCorpus, dir)

	_, err := findCorpus()
	if err == nil {
		t.Fatal("a directory holding no manifest was accepted as a corpus")
	}

	writeErr := os.WriteFile(filepath.Join(dir, manifestName), []byte("[]\n"), 0o600)
	if writeErr != nil {
		t.Fatalf("writing the fixture manifest: %v", writeErr)
	}

	root, err := findCorpus()
	if err != nil {
		t.Fatalf("findCorpus: %v", err)
	}

	if root != dir {
		t.Errorf("findCorpus = %q, want %q", root, dir)
	}
}

func TestFindCorpusFallsBackToTheStageZeroCache(t *testing.T) {
	cache := t.TempDir()

	t.Setenv(envCorpus, "")
	t.Setenv(envCache, cache)

	_, err := findCorpus()
	if err == nil {
		t.Fatal("an empty cache was accepted as a corpus")
	}

	if cacheDir() != cache {
		t.Errorf("cacheDir = %q, want %q", cacheDir(), cache)
	}
}
