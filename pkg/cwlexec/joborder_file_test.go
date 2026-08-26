package cwlexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The SHA-1 digests of the fixture files, computed with sha1sum(1) rather than with this
// package, so that a bug in the digest cannot make the test agree with itself.
const (
	jobSumHello   = "sha1$1f09ca31cc9fc5cf60b67b7d365b7f96682ef267" // the greeting fixture
	jobSumEmpty   = "sha1$da39a3ee5e6b4b0d3255bfef95601890afd80709" // the empty file
	jobSumCshrc   = "sha1$5e3df73c7c6c34b359b85d758a1cc0cfdfea16a7"
	jobSumReadme  = "sha1$0753dda729217d9bd892d252bdd35f2ee6774a5b"
	jobSumArchive = "sha1$3d9f29393afb67270d8be25b947dfc90936a7bc8"
	jobSumBam     = "sha1$49f6005c9dbdfe7f845e2e9a77db530159fed56a"
	jobSumBai     = "sha1$0458ec0efac2a4a5807ca76507cddfa51a769d2b"
	jobSumBig     = "sha1$c470329cc624dc097102eb4fc87a0ecfdf013186" // 70000 bytes of 'x'
)

// jobFileTool is a tool with a single required File input named "f".
func jobFileTool() *cwlcore.CommandLineTool {
	return jobTool(jobParam("f", jobTypeFile))
}

// jobDirTool is a tool with a single required Directory input named "d".
func jobDirTool() *cwlcore.CommandLineTool {
	return jobTool(jobParam("d", jobTypeDirectory))
}

// jobFileWant is the expected shape of a normalised File fixture.
type jobFileWant struct {
	location string
	basename string
	nameroot string
	nameext  string
	checksum string
	size     int64
}

// jobAssertNames checks the four name fields the specification says an implementation must
// derive, plus the identity nameroot + nameext == basename that ties them together.
func jobAssertNames(t *testing.T, file *cwlcore.File, dirname string, want *jobFileWant) {
	t.Helper()

	if file.Basename != want.basename {
		t.Errorf("basename = %q, want %q", file.Basename, want.basename)
	}

	if file.Dirname != dirname {
		t.Errorf("dirname = %q, want %q", file.Dirname, dirname)
	}

	if file.Nameroot != want.nameroot {
		t.Errorf("nameroot = %q, want %q", file.Nameroot, want.nameroot)
	}

	if file.Nameext != want.nameext {
		t.Errorf("nameext = %q, want %q", file.Nameext, want.nameext)
	}

	if file.Nameroot+file.Nameext != file.Basename {
		t.Errorf("nameroot+nameext = %q, want %q", file.Nameroot+file.Nameext, file.Basename)
	}
}

// jobAssertMeasured checks the size and checksum read from disk.
func jobAssertMeasured(t *testing.T, file *cwlcore.File, want *jobFileWant) {
	t.Helper()

	if file.Checksum != want.checksum {
		t.Errorf("checksum = %q, want %q", file.Checksum, want.checksum)
	}

	if !file.Size.IsSet() {
		t.Fatal("size must be set, even when the file is empty")
	}

	if file.Size.Int() != want.size {
		t.Errorf("size = %d, want %d", file.Size.Int(), want.size)
	}
}

func TestFileNameFieldsAreDerivedFromTheBasename(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]jobFileWant{
		"ordinary name": {
			location: "files/" + jobHello + jobExtTxt, basename: jobHello + jobExtTxt,
			nameroot: jobHello, nameext: jobExtTxt, checksum: jobSumHello, size: 12,
		},
		"leading period": {
			// Process.yml: "a basename of `.cshrc` will have a nameroot of `.cshrc`".
			location: "files/" + jobNameCshrc, basename: jobNameCshrc,
			nameroot: jobNameCshrc, nameext: "", checksum: jobSumCshrc, size: 9,
		},
		"no extension": {
			location: "files/" + jobNameReadme, basename: jobNameReadme,
			nameroot: jobNameReadme, nameext: "", checksum: jobSumReadme, size: 7,
		},
		"several periods": {
			// The split is at the *last* period, so nameext holds exactly one.
			location: "files/archive.tar.gz", basename: "archive.tar.gz",
			nameroot: "archive.tar", nameext: ".gz", checksum: jobSumArchive, size: 3,
		},
		"zero bytes": {
			location: "files/" + jobNameEmpty + jobExtTxt, basename: jobNameEmpty + jobExtTxt,
			nameroot: jobNameEmpty, nameext: jobExtTxt, checksum: jobSumEmpty, size: 0,
		},
	}

	for name := range cases {
		want := cases[name]

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			values := jobMustParse(t, fixtures, "f: {class: File, location: "+want.location+"}", jobFileTool())
			file := jobFileValue(t, values, "f")

			jobAssertNames(t, file, filepath.Join(fixtures, filepath.Dir(want.location)), &want)
			jobAssertMeasured(t, file, &want)
		})
	}
}

func TestSplitBasenameEdgeCases(t *testing.T) {
	t.Parallel()

	cases := map[string]joNameParts{
		"":               {root: "", ext: ""},
		".":              {root: ".", ext: ""},
		"..":             {root: "..", ext: ""},
		"...a":           {root: "...a", ext: ""},
		"a.":             {root: "a", ext: "."},
		"a...b":          {root: "a..", ext: ".b"},
		".gitignore.bak": {root: ".gitignore", ext: ".bak"},
	}

	for basename, want := range cases {
		got := joSplitBasename(basename)
		if got != want {
			t.Errorf("joSplitBasename(%q) = %+v, want %+v", basename, got, want)
		}

		if got.root+got.ext != basename {
			t.Errorf("joSplitBasename(%q) does not reassemble", basename)
		}
	}
}

func TestFileAbsoluteLocationIsLeftAlone(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)
	absolute := filepath.Join(fixtures, "files", "hello.txt")

	// The job order is written somewhere unrelated: an absolute location must not be
	// re-based on it.
	values := jobMustParse(t, t.TempDir(), "f: {class: File, location: 'file://"+absolute+"'}", jobFileTool())

	file := jobFileValue(t, values, "f")
	if file.Path != absolute {
		t.Errorf("path = %q, want %q", file.Path, absolute)
	}

	if file.Location != "file://"+absolute {
		t.Errorf("location = %q, want %q", file.Location, "file://"+absolute)
	}
}

func TestFilePathWinsOverLocationAndIsMadeAbsolute(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	src := "f: {class: File, path: files/hello.txt, location: 'file:///nowhere/decoy.txt'}"

	values := jobMustParse(t, fixtures, src, jobFileTool())

	file := jobFileValue(t, values, "f")
	if want := filepath.Join(fixtures, "files", "hello.txt"); file.Path != want {
		t.Errorf("path = %q, want %q", file.Path, want)
	}

	if file.Checksum != jobSumHello {
		t.Errorf("checksum = %q, want the hello.txt digest", file.Checksum)
	}
}

func TestFileAbsolutePathIsCleanedNotRebased(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)
	messy := filepath.Join(fixtures, "files", "..", "files", "hello.txt")

	values := jobMustParse(t, t.TempDir(), "f: {class: File, path: '"+messy+"'}", jobFileTool())

	file := jobFileValue(t, values, "f")
	if want := filepath.Join(fixtures, "files", "hello.txt"); file.Path != want {
		t.Errorf("path = %q, want %q", file.Path, want)
	}
}

func TestFileWithARemoteLocationIsCarriedThroughUnmeasured(t *testing.T) {
	t.Parallel()

	src := "f: {class: File, location: 'https://example.com/data/reads.fastq.gz'}"

	values := jobMustParse(t, t.TempDir(), src, jobFileTool())

	file := jobFileValue(t, values, "f")
	if file.Location != "https://example.com/data/reads.fastq.gz" {
		t.Errorf("location = %q, want the IRI untouched", file.Location)
	}

	if file.Path != "" {
		t.Errorf("path = %q, want empty: the resource is not on this filesystem", file.Path)
	}

	if file.Dirname != "" {
		t.Errorf("dirname = %q, want empty", file.Dirname)
	}

	if file.Basename != "reads.fastq.gz" {
		t.Errorf("basename = %q, want it derived from the IRI", file.Basename)
	}

	if file.Size.IsSet() {
		t.Error("size must stay unset when there is nothing local to measure")
	}
}

func TestFileLocationsAreEscapedAndUnescaped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	name := "a file.txt"

	err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
	if err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	values := jobMustParse(t, dir, "f: {class: File, location: 'a%20file.txt'}", jobFileTool())

	file := jobFileValue(t, values, "f")
	if file.Basename != name {
		t.Errorf("basename = %q, want %q", file.Basename, name)
	}

	if !strings.HasSuffix(file.Location, "/a%20file.txt") {
		t.Errorf("location = %q, want the space re-escaped", file.Location)
	}
}

func TestFileLiteralCarriesContentsSizeAndChecksum(t *testing.T) {
	t.Parallel()

	tool := jobTool(jobParam("f", jobTypeFile))

	values := jobMustParse(t, t.TempDir(), "f: {class: File, contents: \"\", basename: blank.txt}", tool)

	file := jobFileValue(t, values, "f")
	if !file.Contents.IsSet() {
		t.Fatal(`contents: "" is an empty file literal, not an unread file`)
	}

	if file.Contents.Value() != "" {
		t.Errorf("contents = %q, want empty", file.Contents.Value())
	}

	if file.Location != "" || file.Path != "" {
		t.Errorf("a literal must have no location or path, got %q / %q", file.Location, file.Path)
	}

	if !file.Size.IsSet() || file.Size.Int() != 0 {
		t.Errorf("size = %v, want 0", file.Size)
	}

	jobAssertNames(t, file, "", &jobFileWant{basename: "blank.txt", nameroot: "blank", nameext: jobExtTxt})

	if file.Checksum != jobSumEmpty {
		t.Errorf("checksum = %q, want the empty digest", file.Checksum)
	}
}

func TestFileLiteralOverTheSixtyFourKibibyteLimitIsRejected(t *testing.T) {
	t.Parallel()

	oversize := strings.Repeat("x", joMaxContentsBytes+1)

	message := jobMustFail(t, t.TempDir(), "f: {class: File, contents: '"+oversize+"'}", jobFileTool())
	jobWantMessage(t, message, "over the 65536 byte limit")
}

func TestFileNeedsALocationAPathOrContents(t *testing.T) {
	t.Parallel()

	message := jobMustFail(t, t.TempDir(), "f: {class: File, basename: orphan.txt}", jobFileTool())
	jobWantMessage(t, message, "must supply location, path, or contents")
}

func TestMissingFileIsReportedClearly(t *testing.T) {
	t.Parallel()

	message := jobMustFail(t, jobFixtures(t), "f: {class: File, location: files/absent.txt}", jobFileTool())
	jobWantMessage(t, message, "cannot read file")
	jobWantMessage(t, message, "no such file or directory")
}

func TestFileThatNamesADirectoryIsReported(t *testing.T) {
	t.Parallel()

	message := jobMustFail(t, jobFixtures(t), "f: {class: File, location: dir}", jobFileTool())
	jobWantMessage(t, message, "is a directory")
}

func TestFileRejectsAMalformedLocationIRI(t *testing.T) {
	t.Parallel()

	message := jobMustFail(t, t.TempDir(), "f: {class: File, location: '%zz'}", jobFileTool())
	jobWantMessage(t, message, "is not a valid IRI")
}

func TestFileRejectsUnknownAndNonStringFields(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		src  string
		want string
	}{
		jobCaseUnknownField: {
			src:  "f: {class: File, location: files/hello.txt, pathh: x}",
			want: `"pathh" is not a declared File field`,
		},
		"non-string location": {
			src:  "f: {class: File, location: 5}",
			want: "location must be a string",
		},
		"non-string path": {
			src:  "f: {class: File, path: [a]}",
			want: "path must be a string",
		},
		"non-string basename after a failure": {
			// The reader keeps the first diagnostic and skips the rest.
			src:  "f: {class: File, location: 5, basename: 6}",
			want: "location must be a string",
		},
		"non-string contents": {
			src:  "f: {class: File, contents: 5}",
			want: "contents must be a string",
		},
		jobCaseNotAMapping: {
			src:  "f: hello.txt",
			want: "expected a mapping with class: File",
		},
		"wrong class": {
			src:  "f: {class: Directory, location: dir}",
			want: "expected a mapping with class: File",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jobWantMessage(t, jobMustFail(t, jobFixtures(t), tc.src, jobFileTool()), tc.want)
		})
	}
}

func TestFileDerivedFieldsMayBeSuppliedAndAreRecomputed(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	src := "f:\n" +
		"  class: File\n" +
		"  location: files/hello.txt\n" +
		"  checksum: sha1$0000000000000000000000000000000000000000\n" +
		"  size: 999\n" +
		"  dirname: /wrong\n" +
		"  nameroot: wrong\n" +
		"  nameext: .wrong\n"

	file := jobFileValue(t, jobMustParse(t, fixtures, src, jobFileTool()), "f")

	if file.Checksum != jobSumHello {
		t.Errorf("checksum = %q, want it recomputed from disk", file.Checksum)
	}

	if file.Size.Int() != 12 {
		t.Errorf("size = %d, want it recomputed from disk", file.Size.Int())
	}

	if file.Nameroot != jobHello || file.Nameext != jobExtTxt {
		t.Errorf("name fields = %q/%q, want them recomputed", file.Nameroot, file.Nameext)
	}
}

func TestLoadContentsReadsTheFileAndEnforcesTheLimit(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	loading := jobTool(cwlcore.CommandInputParameter{
		ParameterBase: cwlcore.ParameterBase{IDField: "f", Type: jobTypeFile, LoadContents: true},
	})

	t.Run("reads the contents", func(t *testing.T) {
		t.Parallel()

		file := jobFileValue(t, jobMustParse(t, fixtures, "f: {class: File, location: files/hello.txt}", loading), "f")
		if file.Contents.Value() != jobHelloText {
			t.Errorf("contents = %q, want the file's text", file.Contents.Value())
		}
	})

	t.Run("rejects a file over the limit", func(t *testing.T) {
		t.Parallel()

		message := jobMustFail(t, jobOversizeDir(t), jobOversizeSrc, loading)
		jobWantMessage(t, message, "loadContents is set but the file is 70000 bytes")
	})

	t.Run("leaves contents unset when not requested", func(t *testing.T) {
		t.Parallel()

		values := jobMustParse(t, jobOversizeDir(t), jobOversizeSrc, jobFileTool())

		file := jobFileValue(t, values, "f")
		if file.Contents.IsSet() {
			t.Error("contents must stay unset unless loadContents asked for them")
		}

		if file.Checksum != jobSumBig || file.Size.Int() != jobOversizeBytes {
			t.Errorf("the oversize file measured as %q / %d", file.Checksum, file.Size.Int())
		}
	})
}

// jobOversizeBytes is comfortably past the specification's 64 KiB ceiling, and past it by more
// than one [io.Copy] chunk, so that the digest pass also exercises capping its captured head.
const jobOversizeBytes = 70000

// jobOversizeSrc is a job order naming the file jobOversizeDir writes.
const jobOversizeSrc = "f: {class: File, location: oversize.txt}"

// jobOversizeDir writes a file larger than the contents ceiling into a fresh temporary
// directory and returns the directory. It is generated rather than committed because 70 KB of
// one repeated byte is not something a reader ever needs to look at.
func jobOversizeDir(t *testing.T) string {
	t.Helper()

	return jobWriteFile(t, "oversize.txt", strings.Repeat("x", jobOversizeBytes))
}

func TestSecondaryFilesSuppliedByTheJobAreResolved(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	src := "f:\n" +
		"  class: File\n" +
		"  location: files/data.bam\n" +
		"  secondaryFiles:\n" +
		"    - {class: File, location: files/data.bam.bai}\n" +
		"    - {class: Directory, location: dir}\n"

	file := jobFileValue(t, jobMustParse(t, fixtures, src, jobFileTool()), "f")

	if file.Checksum != jobSumBam {
		t.Errorf("primary checksum = %q", file.Checksum)
	}

	if len(file.SecondaryFiles) != 2 {
		t.Fatalf("secondaryFiles = %d entries, want 2", len(file.SecondaryFiles))
	}

	index, ok := file.SecondaryFiles[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("secondaryFiles[0] is %T, want *cwlcore.File", file.SecondaryFiles[0])
	}

	if index.Checksum != jobSumBai {
		t.Errorf("secondary checksum = %q, want the .bai digest", index.Checksum)
	}

	if index.Basename != "data.bam.bai" || index.Nameroot != "data.bam" || index.Nameext != jobExtBai {
		t.Errorf("secondary name fields = %q/%q/%q", index.Basename, index.Nameroot, index.Nameext)
	}

	if _, ok := file.SecondaryFiles[1].(*cwlcore.Directory); !ok {
		t.Errorf("secondaryFiles[1] is %T, want *cwlcore.Directory", file.SecondaryFiles[1])
	}
}

func TestSecondaryFilesAcceptASingleEntry(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	src := "f:\n" +
		"  class: File\n" +
		"  location: files/data.bam\n" +
		"  secondaryFiles: {class: File, location: files/data.bam.bai}\n"

	file := jobFileValue(t, jobMustParse(t, fixtures, src, jobFileTool()), "f")
	if len(file.SecondaryFiles) != 1 {
		t.Fatalf("secondaryFiles = %d entries, want 1", len(file.SecondaryFiles))
	}
}

func TestSuppliedSecondaryFilesSurviveAlongsideDiscoveredOnes(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	// The values a job order supplies are resolved by the conversion pass; the patterns a
	// parameter declares are applied by the discovery pass afterwards. Both end up on the same
	// File, and the supplied entry keeps its place at the front.
	tool := jobTool(cwlcore.CommandInputParameter{
		ParameterBase: cwlcore.ParameterBase{
			IDField:        "f",
			Type:           jobTypeFile,
			SecondaryFiles: []cwlcore.SecondaryFileSchema{{Pattern: jobExtBai}},
		},
	})

	src := "f:\n" +
		"  class: File\n" +
		"  location: files/data.bam\n" +
		"  secondaryFiles:\n" +
		"    - {class: Directory, location: dir}\n"

	file := jobFileValue(t, jobMustParse(t, fixtures, src, tool), "f")
	if len(file.SecondaryFiles) != 2 {
		t.Fatalf("secondaryFiles = %d entries, want the supplied one and the discovered one", len(file.SecondaryFiles))
	}

	if got := basenameOf(file.SecondaryFiles[0]); got != jobDirName {
		t.Errorf("secondaryFiles[0] = %q, want the supplied entry", got)
	}

	if got := basenameOf(file.SecondaryFiles[1]); got != "data.bam.bai" {
		t.Errorf("secondaryFiles[1] = %q, want the discovered index", got)
	}
}

func TestSecondaryFilesRejectMalformedEntries(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]struct {
		src  string
		want string
	}{
		"scalar entry": {
			src:  "f: {class: File, location: files/data.bam, secondaryFiles: [data.bam.bai]}",
			want: "expected a mapping with class: File or class: Directory",
		},
		"unknown class": {
			src:  "f: {class: File, location: files/data.bam, secondaryFiles: [{class: Widget}]}",
			want: "must declare class: File or class: Directory",
		},
		"broken entry": {
			src:  "f: {class: File, location: files/data.bam, secondaryFiles: [{class: File}]}",
			want: "must supply location, path, or contents",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			message := jobMustFail(t, fixtures, tc.src, jobFileTool())
			jobWantMessage(t, message, tc.want)
			jobWantMessage(t, message, "f.secondaryFiles[0]")
		})
	}
}

func TestDirectoryWithoutAListingKeepsItNil(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	dir := jobDirValue(t, jobMustParse(t, fixtures, "d: {class: Directory, location: dir}", jobDirTool()))

	if dir.Listing != nil {
		t.Errorf("listing = %v, want nil: an absent listing is read from the location later", dir.Listing)
	}

	if want := filepath.Join(fixtures, "dir"); dir.Path != want {
		t.Errorf("path = %q, want %q", dir.Path, want)
	}

	if dir.Basename != "dir" {
		t.Errorf("basename = %q, want %q", dir.Basename, "dir")
	}
}

func TestDirectoryWithAnExplicitListingIsNormalised(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	src := "d:\n" +
		"  class: Directory\n" +
		"  location: dir\n" +
		"  listing:\n" +
		"    - {class: File, location: dir/one.txt}\n" +
		"    - {class: Directory, location: dir/sub}\n"

	dir := jobDirValue(t, jobMustParse(t, fixtures, src, jobDirTool()))

	if len(dir.Listing) != 2 {
		t.Fatalf("listing = %d entries, want 2", len(dir.Listing))
	}

	entry, ok := dir.Listing[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("listing[0] is %T, want *cwlcore.File", dir.Listing[0])
	}

	if entry.Basename != "one.txt" {
		t.Errorf("listing[0].basename = %q", entry.Basename)
	}

	sub, ok := dir.Listing[1].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("listing[1] is %T, want *cwlcore.Directory", dir.Listing[1])
	}

	const wantSub = "sub"

	if sub.Basename != wantSub {
		t.Errorf("listing[1].basename = %q, want %q", sub.Basename, wantSub)
	}
}

func TestDirectoryLiteralNeedsNoLocation(t *testing.T) {
	t.Parallel()

	values := jobMustParse(t, t.TempDir(),
		"d: {class: Directory, basename: made_up, listing: []}", jobDirTool())

	dir := jobDirValue(t, values)
	if dir.Listing == nil {
		t.Fatal("an explicitly empty listing is not the same as an absent one")
	}

	if len(dir.Listing) != 0 {
		t.Errorf("listing = %v, want empty", dir.Listing)
	}

	if dir.Location != "" || dir.Path != "" {
		t.Errorf("a directory literal must have no location or path, got %q / %q", dir.Location, dir.Path)
	}
}

func TestDirectoryErrors(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]struct {
		src  string
		want string
	}{
		"neither location nor listing": {
			src:  "d: {class: Directory, basename: nowhere}",
			want: "must supply location, path, or listing",
		},
		"missing on disk": {
			src:  "d: {class: Directory, location: absent}",
			want: "no such file or directory",
		},
		"names a file": {
			src:  "d: {class: Directory, location: files/hello.txt}",
			want: "is not a directory",
		},
		jobCaseUnknownField: {
			src:  "d: {class: Directory, location: dir, checksum: sha1$0}",
			want: `"checksum" is not a declared Directory field`,
		},
		"non-string basename": {
			src:  "d: {class: Directory, location: dir, basename: 5}",
			want: "basename must be a string",
		},
		jobCaseNotAMapping: {
			src:  "d: [dir]",
			want: "expected a mapping with class: Directory",
		},
		"broken listing entry": {
			src:  "d: {class: Directory, location: dir, listing: [{class: File}]}",
			want: "must supply location, path, or contents",
		},
		"null listing is absent": {
			src:  "d: {class: Directory, listing: null}",
			want: "must supply location, path, or listing",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jobWantMessage(t, jobMustFail(t, fixtures, tc.src, jobDirTool()), tc.want)
		})
	}
}

func TestDirectoryDerivesBasenameFromAnExplicitValue(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	values := jobMustParse(t, fixtures, "d: {class: Directory, location: dir, basename: "+jobRenamed+"}", jobDirTool())
	if got := jobDirValue(t, values).Basename; got != jobRenamed {
		t.Errorf("basename = %q, want %q", got, "renamed")
	}
}

func TestCancellationIsReportedForADirectoryToo(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := ParseJobOrder(ctx, filepath.Join(jobFixtures(t), "job.yml"),
		[]byte("d: {class: Directory, location: dir}"), jobDirTool())
	if err == nil {
		t.Fatal("expected the cancelled context to stop the load")
	}

	jobWantMessage(t, jobPretty(t, err), "context canceled")
}
