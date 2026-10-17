package cwlexec

import (
	"errors"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

func validFile(path, location string) *cwlcore.File {
	f := &cwlcore.File{Basename: outNameX}
	f.SetPathAndLocation(path, location)

	return f
}

func validDir(path, location string) *cwlcore.Directory {
	d := &cwlcore.Directory{Basename: "d"}
	d.SetPathAndLocation(path, location)

	return d
}

func TestValidateOutputPathsAcceptsConsistentValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		outputs map[string]any
	}{
		{
			name:    "local path and file location",
			outputs: map[string]any{outPort: validFile("/data/x.txt", "file:///data/x.txt")},
		},
		{
			name:    "remote path and remote location",
			outputs: map[string]any{outPort: validFile("s3://bucket/x.txt", "s3://bucket/x.txt")},
		},
		{
			name: "path only",
			outputs: map[string]any{outPort: func() *cwlcore.File {
				f := &cwlcore.File{Basename: outNameX}
				f.SetPath("s3://bucket/x.txt")

				return f
			}()},
		},
		{
			name: "location only",
			outputs: map[string]any{outPort: func() *cwlcore.File {
				f := &cwlcore.File{Basename: outNameX}
				f.SetLocation("file:///data/x.txt")

				return f
			}()},
		},
		{
			name: "nil values",
			outputs: map[string]any{
				"f": (*cwlcore.File)(nil),
				"d": (*cwlcore.Directory)(nil),
			},
		},
		{
			name:    "empty outputs",
			outputs: make(map[string]any),
		},
		{
			name:    "scalar values",
			outputs: map[string]any{"msg": "hello"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateOutputPaths(testCase.outputs)
			if err != nil {
				t.Errorf("ValidateOutputPaths: unexpected error: %v", err)
			}
		})
	}
}

func TestValidateOutputPathsRejectsInconsistentSchemes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		outputs map[string]any
	}{
		{
			name:    "s3 path with file location",
			outputs: map[string]any{outPort: validFile("s3://bucket/x.txt", "file:///tmp/stale/x.txt")},
		},
		{
			name:    "directory with mixed schemes",
			outputs: map[string]any{jobDirName: validDir("s3://bucket/ledger", "file:///tmp/old/ledger")},
		},
		{
			name: "inconsistent secondary file",
			outputs: map[string]any{
				"out": func() *cwlcore.File {
					f := validFile("s3://bucket/x.bam", "s3://bucket/x.bam")
					f.SecondaryFiles = []cwlcore.FileOrDirectory{
						validFile("s3://bucket/x.bai", "file:///tmp/x.bai"),
					}

					return f
				}(),
			},
		},
		{
			name: "inconsistent listing entry",
			outputs: map[string]any{
				jobDirName: func() *cwlcore.Directory {
					d := validDir("s3://bucket/d", "s3://bucket/d")
					d.Listing = []cwlcore.FileOrDirectory{
						validFile("s3://bucket/d/f.txt", "file:///tmp/f.txt"),
					}

					return d
				}(),
			},
		},
		{
			name: "nested in array",
			outputs: map[string]any{
				"arr": []any{validFile("s3://bucket/a.txt", "file:///tmp/a.txt")},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateOutputPaths(testCase.outputs)
			if !errors.Is(err, ErrInconsistentPaths) {
				t.Errorf("ValidateOutputPaths: error = %v, want ErrInconsistentPaths", err)
			}
		})
	}
}
