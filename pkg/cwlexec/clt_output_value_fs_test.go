package cwlexec

import (
	"testing"
	"testing/fstest"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

const testDataBin = "data.bin"

func TestOutRemeasureFS(t *testing.T) {
	t.Parallel()

	content := []byte("measured via FS")
	mfs := mapWriteFS{fstest.MapFS{
		testDataBin: &fstest.MapFile{Data: content},
	}}

	file := &cwlcore.File{
		Node:           nil,
		Location:       "",
		Path:           fakeOutdir + "/" + testDataBin,
		Basename:       testDataBin,
		Dirname:        "",
		Nameroot:       "",
		Nameext:        "",
		Checksum:       "",
		Format:         "",
		Size:           cwlcore.OptInt{},
		Contents:       cwlcore.OptString{},
		SecondaryFiles: nil,
	}

	outRemeasure(file, mfs, fakeOutdir)

	if !file.Size.IsSet() || file.Size.Int() != int64(len(content)) {
		t.Errorf("size = %v, want %d", file.Size, len(content))
	}

	wantChecksum := outChecksumOf(content)
	if file.Checksum != wantChecksum {
		t.Errorf("checksum = %q, want %q", file.Checksum, wantChecksum)
	}
}

func TestOutRemeasureSkipsPathOutsideOutdir(t *testing.T) {
	t.Parallel()

	file := &cwlcore.File{
		Node:           nil,
		Location:       "",
		Path:           "/somewhere/else/" + testDataBin,
		Basename:       testDataBin,
		Dirname:        "",
		Nameroot:       "",
		Nameext:        "",
		Checksum:       "",
		Format:         "",
		Size:           cwlcore.OptInt{},
		Contents:       cwlcore.OptString{},
		SecondaryFiles: nil,
	}

	mfs := mapWriteFS{fstest.MapFS{}}
	outRemeasure(file, mfs, fakeOutdir)

	if file.Size.IsSet() {
		t.Error("should not measure a file outside outdir")
	}

	if file.Checksum != "" {
		t.Error("should not set checksum for a file outside outdir")
	}
}
