package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The sha1 digests the specification's checksum field carries for the two literals the tests below
// write, so that an assertion names the value rather than recomputing it the way the code does.
const (
	helloDigest = "sha1$fea23663b9c8ed71968f86415b5ec091bb111448"
	emptyDigest = "sha1$da39a3ee5e6b4b0d3255bfef95601890afd80709"
)

// helloLiteral is the file literal from the conformance suite's file-literal-ex.cwl.
const helloLiteral = "Hello file literal."

// literalCall builds a one-port ExpressionTool call whose expression is a JavaScript function body,
// with an output directory of its own to write into.
func literalCall(t *testing.T, expr string) *StepCall {
	t.Helper()

	return &StepCall{
		StepID:       stepID,
		Process:      newExpressionTool(expr, outID),
		Class:        Class(cwlcore.ClassExpressionTool),
		Requirements: jsScope(nil),
		OutDir:       t.TempDir(),
	}
}

// runLiteralTool runs a call that must succeed and returns the value its single port holds.
func runLiteralTool(t *testing.T, call *StepCall) any {
	t.Helper()

	result, err := runExpressionTool(t, call)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	return result.Outputs[outPort]
}

// wantFile asserts that a value is a typed File, which is the representation every output object in
// this engine carries.
func wantFile(t *testing.T, value any) *cwlcore.File {
	t.Helper()

	file, ok := value.(*cwlcore.File)
	if !ok {
		t.Fatalf("value = %#v, want a *cwlcore.File", value)
	}

	return file
}

// wantDirectory asserts that a value is a typed Directory.
func wantDirectory(t *testing.T, value any) *cwlcore.Directory {
	t.Helper()

	dir, ok := value.(*cwlcore.Directory)
	if !ok {
		t.Fatalf("value = %#v, want a *cwlcore.Directory", value)
	}

	return dir
}

// assertFileOnDisk checks that a File names a real file holding want, under the location and path a
// materialized value must carry.
func assertFileOnDisk(t *testing.T, file *cwlcore.File, want string) {
	t.Helper()

	got, err := os.ReadFile(filepath.Clean(file.Path))
	if err != nil {
		t.Fatalf("reading the materialized literal: %v", err)
	}

	if string(got) != want {
		t.Fatalf("contents of %s = %q, want %q", file.Path, got, want)
	}

	if file.Location != "file://"+file.Path {
		t.Fatalf("location = %q, want the file IRI of %q", file.Location, file.Path)
	}
}

// assertIsDir checks that a path names a directory that exists.
func assertIsDir(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	if !info.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
}

// assertOutDirEmpty checks that nothing was written into an invocation's output directory.
func assertOutDirEmpty(t *testing.T, outdir string) {
	t.Helper()

	entries, err := os.ReadDir(outdir)
	if err != nil {
		t.Fatalf("reading %s: %v", outdir, err)
	}

	if len(entries) != 0 {
		t.Fatalf("output directory holds %v, want nothing written into it", entries)
	}
}

func TestExpressionToolWritesAFileLiteral(t *testing.T) {
	t.Parallel()

	call := literalCall(t, fmt.Sprintf(
		`${return {"out": {"class": "File", "basename": "a_file", "contents": %q}};}`, helloLiteral))

	file := wantFile(t, runLiteralTool(t, call))
	assertFileOnDisk(t, file, helloLiteral)

	if file.Basename != "a_file" {
		t.Fatalf("basename = %q, want the one the expression chose", file.Basename)
	}

	if file.Dirname != call.OutDir {
		t.Fatalf("literal written to %q, want it in the invocation's output directory %q", file.Dirname, call.OutDir)
	}

	if !file.Size.IsSet() || file.Size.Int() != int64(len(helloLiteral)) {
		t.Fatalf("size = %#v, want %d", file.Size, len(helloLiteral))
	}

	if file.Checksum != helloDigest {
		t.Fatalf("checksum = %q, want %q", file.Checksum, helloDigest)
	}
}

func TestExpressionToolWritesAnEmptyFileLiteral(t *testing.T) {
	t.Parallel()

	// File.contents is an OptString precisely so that this case survives: "" is an empty file
	// literal that must be created, not an unread file.
	call := literalCall(t, `${return {"out": {"class": "File", "basename": "e", "contents": ""}};}`)

	file := wantFile(t, runLiteralTool(t, call))
	assertFileOnDisk(t, file, "")

	if !file.Size.IsSet() || file.Size.Int() != 0 {
		t.Fatalf("size = %#v, want 0", file.Size)
	}

	if file.Checksum != emptyDigest {
		t.Fatalf("checksum = %q, want the digest of no bytes", file.Checksum)
	}
}

func TestExpressionToolNamesALiteralThatNamesItselfNothing(t *testing.T) {
	t.Parallel()

	// Process.yml requires a basename on every File; one the expression did not supply has to be
	// invented, because a value nothing can name is a value nothing can open.
	call := literalCall(t, `${return {"out": {"class": "File", "contents": "x"}};}`)

	file := wantFile(t, runLiteralTool(t, call))
	assertFileOnDisk(t, file, "x")

	if file.Basename == "" {
		t.Fatal("a materialized literal must be named")
	}
}

func TestExpressionToolWritesADirectoryLiteral(t *testing.T) {
	t.Parallel()

	// The third entry is not a literal: it names a directory that exists, so it is placed rather
	// than created, and nothing about it is invented.
	existing := t.TempDir()

	call := literalCall(t, fmt.Sprintf(`${return {"out": {"class": "Directory", "basename": "a_directory", "listing": [
		{"class": "File", "basename": "f.txt", "contents": "inside"},
		{"class": "Directory", "basename": "sub", "listing": []},
		{"class": "Directory", "basename": "kept", "location": %q}
	]}};}`, "file://"+existing))

	dir := wantDirectory(t, runLiteralTool(t, call))
	if filepath.Base(dir.Path) != "a_directory" {
		t.Fatalf("path = %q, want a directory named after the literal", dir.Path)
	}

	assertIsDir(t, dir.Path)

	if len(dir.Listing) != 3 {
		t.Fatalf("listing = %#v, want the three entries the expression wrote", dir.Listing)
	}

	entry := wantFile(t, dir.Listing[0])
	assertFileOnDisk(t, entry, "inside")

	if entry.Dirname != dir.Path {
		t.Fatalf("entry written to %q, want it inside %q", entry.Dirname, dir.Path)
	}

	assertIsDir(t, wantDirectory(t, dir.Listing[1]).Path)
	assertIsDir(t, wantDirectory(t, dir.Listing[2]).Path)
}

func TestExpressionToolWritesALiteralSecondaryFile(t *testing.T) {
	t.Parallel()

	call := literalCall(t, `${return {"out": {"class": "File", "basename": "p.txt", "contents": "primary",
		"secondaryFiles": [{"class": "File", "basename": "p.txt.idx", "contents": "index"}]}};}`)

	file := wantFile(t, runLiteralTool(t, call))
	assertFileOnDisk(t, file, "primary")

	if len(file.SecondaryFiles) != 1 {
		t.Fatalf("secondaryFiles = %#v, want the one the expression wrote", file.SecondaryFiles)
	}

	secondary := wantFile(t, file.SecondaryFiles[0])
	assertFileOnDisk(t, secondary, "index")

	if secondary.Dirname != file.Dirname {
		t.Fatal("a secondary file must be written beside the file it belongs to")
	}
}

func TestExpressionToolWritesNothingWhenNoLiteralIsReturned(t *testing.T) {
	t.Parallel()

	// A File or Directory the expression passed through already has bytes on a filesystem, so
	// there is nothing to create and nothing to put in the output directory.
	file := &cwlcore.File{Location: "file:///w/in.txt", Path: "/w/in.txt", Basename: "in.txt"}
	dir := &cwlcore.Directory{Location: "file:///w/d", Path: "/w/d", Basename: "d"}

	call := &StepCall{
		StepID:       stepID,
		Process:      newExpressionTool("$(inputs.obj)", outID, extraID),
		Inputs:       map[string]any{"obj": map[string]any{outPort: []any{file, dir}, extraPort: "passthrough"}},
		Requirements: jsScope(nil),
		OutDir:       t.TempDir(),
	}

	result, err := runExpressionTool(t, call)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Outputs[extraPort] != "passthrough" {
		t.Fatalf("outputs = %#v, want the object the expression produced", result.Outputs)
	}

	list, ok := result.Outputs[outPort].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("output %q = %#v, want the two-element list the expression produced", outPort, result.Outputs[outPort])
	}

	if wantFile(t, list[0]).Path != file.Path || wantDirectory(t, list[1]).Path != dir.Path {
		t.Fatalf("output %q = %#v, want the values that went in", outPort, list)
	}

	assertOutDirEmpty(t, call.OutDir)
}

func TestExpressionToolReachesALiteralInsideARecord(t *testing.T) {
	t.Parallel()

	// A record is not a filesystem value, but it may hold one, and an expression is free to nest
	// its result however the declared output type allows.
	call := literalCall(t, `${return {"out": {"nested": {"class": "File", "basename": "n.txt", "contents": "deep"}}};}`)

	record, ok := runLiteralTool(t, call).(map[string]any)
	if !ok {
		t.Fatal("the record the expression returned must survive as a record")
	}

	assertFileOnDisk(t, wantFile(t, record["nested"]), "deep")
}

func TestExpressionToolRejectsAnOversizedLiteral(t *testing.T) {
	t.Parallel()

	call := literalCall(t, fmt.Sprintf(
		`${return {"out": {"class": "File", "basename": "big", "contents": %q}};}`,
		strings.Repeat("x", joMaxContentsBytes+1)))

	_, err := runExpressionTool(t, call)
	if !errors.Is(err, ErrLiteralTooLarge) {
		t.Fatalf("error = %v, want ErrLiteralTooLarge", err)
	}
}

func TestExpressionToolReportsOnlyTheFirstOversizedLiteral(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", joMaxContentsBytes+1)
	call := literalCall(t, fmt.Sprintf(`${return {"out": {"class": "Directory", "basename": "d", "listing": [
		{"class": "File", "basename": "first", "contents": %q},
		{"class": "File", "basename": "second", "contents": %q}
	]}};}`, oversized, oversized))

	_, err := runExpressionTool(t, call)
	if !errors.Is(err, ErrLiteralTooLarge) {
		t.Fatalf("error = %v, want ErrLiteralTooLarge", err)
	}

	if strings.Contains(err.Error(), "second") {
		t.Fatalf("error %q reports past the first failure", err)
	}
}

func TestExpressionToolRejectsAMalformedFilesystemObject(t *testing.T) {
	t.Parallel()

	call := literalCall(t, `${return {"out": {"class": "File", "location": "file:///w/x", "size": "big"}};}`)

	_, err := runExpressionTool(t, call)
	if !errors.Is(err, cwlcore.ErrExpressionEval) {
		t.Fatalf("error = %v, want ErrExpressionEval", err)
	}

	if !strings.Contains(err.Error(), outPort) {
		t.Fatalf("error %q does not name the offending output port", err)
	}
}

func TestExpressionToolRejectsAnUnusableOutputDirectory(t *testing.T) {
	t.Parallel()

	call := literalCall(t, `${return {"out": {"class": "File", "basename": "a", "contents": "x"}};}`)
	call.OutDir = "not/absolute"

	_, err := runExpressionTool(t, call)
	if !errors.Is(err, ErrInvocationDir) {
		t.Fatalf("error = %v, want ErrInvocationDir", err)
	}
}

func TestExpressionToolRejectsALiteralItCannotPlace(t *testing.T) {
	t.Parallel()

	// The directory literal is one this engine can create; the entry it lists names a resource on
	// a filesystem this engine cannot read, so the placement cannot be planned.
	call := literalCall(t, `${return {"out": {"class": "Directory", "basename": "d", "listing": [
		{"class": "File", "location": "http://example.invalid/x", "basename": "x"}
	]}};}`)

	_, err := runExpressionTool(t, call)
	if !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("error = %v, want ErrUnsupportedFeature", err)
	}
}

func TestExpressionToolReportsAFailedPlacement(t *testing.T) {
	t.Parallel()

	// The second entry's basename puts it underneath the first, which by then is a regular file.
	// The plan is well-formed; the filesystem is what refuses it.
	call := literalCall(t, `${return {"out": {"class": "Directory", "basename": "d", "listing": [
		{"class": "File", "basename": "x", "contents": ""},
		{"class": "File", "basename": "x/y", "contents": "z"}
	]}};}`)

	_, err := runExpressionTool(t, call)
	if err == nil {
		t.Fatal("a placement the filesystem refused must be reported")
	}

	if !strings.Contains(err.Error(), "staging") {
		t.Fatalf("error %q does not say that staging failed", err)
	}
}

func TestExpressionToolHonoursCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	call := literalCall(t, `${return {"out": {"class": "File", "basename": "a", "contents": "x"}};}`)

	_, err := Outcome(expressionToolBuiltIn(t).Execute(ctx, call))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	assertOutDirEmpty(t, call.OutDir)
}
