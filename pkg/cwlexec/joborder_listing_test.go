package cwlexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The three-step `loadListing` precedence, applied to the Directory values in an input object.
//
// The fixture tree under testdata/joborder/dir is two levels deep — one.txt and sub/, with
// sub/two.txt inside — which is what makes shallow and deep distinguishable: a shallow walk names
// sub without reading it, a deep walk reads it.

// jobListingTool is a tool with a single Directory input "d" carrying the given loadListing, and a
// LoadListingRequirement carrying required. Either may be empty, which is how each step of the
// precedence is isolated.
func jobListingTool(own, required cwlcore.LoadListingEnum) *cwlcore.CommandLineTool {
	param := jobParam("d", jobTypeDirectory)
	param.LoadListing = own

	tool := jobTool(param)
	if required != "" {
		tool.Requirements = []cwlcore.ProcessRequirement{
			&cwlcore.LoadListingRequirement{LoadListing: required},
		}
	}

	return tool
}

// jobSubdirectory asserts that a listing's sub/ entry is a Directory and returns it.
func jobSubdirectory(t *testing.T, listing []cwlcore.FileOrDirectory) *cwlcore.Directory {
	t.Helper()

	if len(listing) != 2 {
		t.Fatalf("listing has %d entries, want the fixture's two", len(listing))
	}

	sub, ok := listing[1].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("listing[1] is %T, want *cwlcore.Directory", listing[1])
	}

	return sub
}

func TestInputDirectoryListingFollowsTheLoadListingPrecedence(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]struct {
		own      cwlcore.LoadListingEnum
		required cwlcore.LoadListingEnum
		want     cwlcore.LoadListingEnum
	}{
		"nothing declared falls back to no_listing": {want: cwlcore.LoadListingNone},
		"an explicit no_listing": {
			own:  cwlcore.LoadListingNone,
			want: cwlcore.LoadListingNone,
		},
		"the requirement alone": {
			required: cwlcore.LoadListingShallow,
			want:     cwlcore.LoadListingShallow,
		},
		"the requirement, deep": {
			required: cwlcore.LoadListingDeep,
			want:     cwlcore.LoadListingDeep,
		},
		"the parameter alone": {
			own:  cwlcore.LoadListingShallow,
			want: cwlcore.LoadListingShallow,
		},
		"the parameter beats the requirement": {
			own:      cwlcore.LoadListingDeep,
			required: cwlcore.LoadListingShallow,
			want:     cwlcore.LoadListingDeep,
		},
		"the parameter's no_listing beats the requirement too": {
			own:      cwlcore.LoadListingNone,
			required: cwlcore.LoadListingDeep,
			want:     cwlcore.LoadListingNone,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			values := jobMustParse(t, fixtures, "d: {class: Directory, location: dir}",
				jobListingTool(tc.own, tc.required))

			jobAssertListedTo(t, jobDirValue(t, values), tc.want)
		})
	}
}

// jobAssertListedTo checks a Directory against the shape each loadListing setting produces.
func jobAssertListedTo(t *testing.T, dir *cwlcore.Directory, mode cwlcore.LoadListingEnum) {
	t.Helper()

	if mode == cwlcore.LoadListingNone {
		if dir.Listing != nil {
			t.Errorf("listing = %v, want nil: no_listing means nobody read it", dir.Listing)
		}

		return
	}

	sub := jobSubdirectory(t, dir.Listing)

	if mode == cwlcore.LoadListingShallow {
		if sub.Listing != nil {
			t.Errorf("sub listing = %v, want nil: a shallow walk names a subdirectory without reading it", sub.Listing)
		}

		return
	}

	if len(sub.Listing) != 1 {
		t.Fatalf("sub listing has %d entries, want the one a deep walk reaches", len(sub.Listing))
	}

	if got := basenameOf(sub.Listing[0]); got != "two.txt" {
		t.Errorf("sub listing[0] = %q, want %q", got, "two.txt")
	}
}

func TestAReadListingCarriesTheFieldsAFileValueMustHave(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	values := jobMustParse(t, fixtures, "d: {class: Directory, location: dir}",
		jobListingTool(cwlcore.LoadListingShallow, ""))

	dir := jobDirValue(t, values)

	file, ok := dir.Listing[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("listing[0] is %T, want *cwlcore.File", dir.Listing[0])
	}

	if want := filepath.Join(fixtures, "dir", "one.txt"); file.Path != want {
		t.Errorf("path = %q, want %q", file.Path, want)
	}

	// A listed File goes through the same measurement an explicitly supplied one does, so an
	// expression reading self.size or self.checksum sees the same answer either way.
	if !file.Size.IsSet() || file.Checksum == "" {
		t.Errorf("size = %v, checksum = %q, want both read from disk", file.Size, file.Checksum)
	}
}

func TestAnExplicitListingIsNeverOverwritten(t *testing.T) {
	t.Parallel()

	// A job order that supplies a listing has said what the directory contains; deep_listing
	// must not go behind it and replace the answer with what happens to be on disk.
	src := "d:\n" +
		"  class: Directory\n" +
		"  location: dir\n" +
		"  listing:\n" +
		"    - {class: File, location: files/hello.txt}\n"

	dir := jobDirValue(t, jobMustParse(t, jobFixtures(t), src, jobListingTool(cwlcore.LoadListingDeep, "")))

	if len(dir.Listing) != 1 {
		t.Fatalf("listing has %d entries, want the one the job order supplied", len(dir.Listing))
	}

	if got := basenameOf(dir.Listing[0]); got != jobHello+jobExtTxt {
		t.Errorf("listing[0] = %q, want %q", got, jobHello+jobExtTxt)
	}
}

func TestAnExplicitlyEmptyListingStaysEmpty(t *testing.T) {
	t.Parallel()

	dir := jobDirValue(t, jobMustParse(t, jobFixtures(t),
		"d: {class: Directory, location: dir, listing: []}", jobListingTool(cwlcore.LoadListingDeep, "")))

	if dir.Listing == nil {
		t.Fatal("listing = nil, want the empty one the job order supplied: [] asserts the directory is empty")
	}

	if len(dir.Listing) != 0 {
		t.Errorf("listing = %v, want it empty", dir.Listing)
	}
}

func TestADirectoryLiteralIsNotWalked(t *testing.T) {
	t.Parallel()

	// A literal has no location, so there is nothing on disk to read a listing from; the one it
	// declares is the whole of it.
	dir := jobDirValue(t, jobMustParse(t, jobFixtures(t),
		"d: {class: Directory, basename: made_up, listing: []}", jobListingTool(cwlcore.LoadListingDeep, "")))

	if dir.Path != "" {
		t.Errorf("path = %q, want it empty for a literal", dir.Path)
	}
}

func TestLoadListingReachesADirectoryInsideARecordAndAnArray(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)
	dirs := cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeDirectory})

	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{
			{Name: "shallow", Type: jobTypeDirectory, LoadListing: cwlcore.LoadListingShallow},
			{Name: "inherited", Type: dirs},
		},
	})

	tool := jobTool(jobParam("r", record))
	tool.Requirements = []cwlcore.ProcessRequirement{
		&cwlcore.LoadListingRequirement{LoadListing: cwlcore.LoadListingDeep},
	}

	src := "r:\n" +
		"  shallow: {class: Directory, location: dir}\n" +
		"  inherited: [{class: Directory, location: dir}]\n"

	record0, ok := jobMustParse(t, fixtures, src, tool)["r"].(map[string]any)
	if !ok {
		t.Fatal(`input "r" is not a record`)
	}

	shallow, ok := record0["shallow"].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("shallow is %T, want *cwlcore.Directory", record0["shallow"])
	}

	jobAssertListedTo(t, shallow, cwlcore.LoadListingShallow)

	items, ok := record0["inherited"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("inherited is %T, want a one-element array", record0["inherited"])
	}

	inherited, ok := items[0].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("inherited[0] is %T, want *cwlcore.Directory", items[0])
	}

	jobAssertListedTo(t, inherited, cwlcore.LoadListingDeep)
}

func TestAnUnreadableDirectoryIsReportedFromTheListingWalk(t *testing.T) {
	t.Parallel()
	stgSkipIfRoot(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "closed")

	err := os.Mkdir(target, 0o700)
	if err != nil {
		t.Fatalf("creating a directory: %v", err)
	}

	// The directory stats as a directory and then refuses to be listed, which is the one way
	// the walk can fail after everything before it has succeeded.
	err = os.Chmod(target, 0o000)
	if err != nil {
		t.Fatalf("making a directory unreadable: %v", err)
	}

	t.Cleanup(func() { stgRestore(t, target, 0o700) })

	message := jobMustFail(t, dir, "d: {class: Directory, location: closed}",
		jobListingTool(cwlcore.LoadListingShallow, ""))

	jobWantMessage(t, message, "reading the directory listing")
}
