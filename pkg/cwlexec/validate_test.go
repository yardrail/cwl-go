package cwlexec

import (
	"errors"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

func TestValidateOutputPathsAcceptsConsistentValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		outputs map[string]any
	}{
		{
			name: "local path and file location",
			outputs: map[string]any{
				"out": &cwlcore.File{Path: "/data/x.txt", Location: "file:///data/x.txt", Basename: "x.txt"},
			},
		},
		{
			name: "remote path and remote location",
			outputs: map[string]any{
				"out": &cwlcore.File{Path: "s3://bucket/x.txt", Location: "s3://bucket/x.txt", Basename: "x.txt"},
			},
		},
		{
			name: "path only",
			outputs: map[string]any{
				"out": &cwlcore.File{Path: "s3://bucket/x.txt", Basename: "x.txt"},
			},
		},
		{
			name: "location only",
			outputs: map[string]any{
				"out": &cwlcore.File{Location: "file:///data/x.txt", Basename: "x.txt"},
			},
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
			name: "scalar values",
			outputs: map[string]any{
				"msg": "hello",
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateOutputPaths(testCase.outputs); err != nil {
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
			name: "s3 path with file location",
			outputs: map[string]any{
				"out": &cwlcore.File{
					Path: "s3://bucket/x.txt", Location: "file:///tmp/stale/x.txt", Basename: "x.txt",
				},
			},
		},
		{
			name: "directory with mixed schemes",
			outputs: map[string]any{
				"dir": &cwlcore.Directory{
					Path: "s3://bucket/ledger", Location: "file:///tmp/old/ledger", Basename: "ledger",
				},
			},
		},
		{
			name: "inconsistent secondary file",
			outputs: map[string]any{
				"out": &cwlcore.File{
					Path: "s3://bucket/x.bam", Location: "s3://bucket/x.bam", Basename: "x.bam",
					SecondaryFiles: []cwlcore.FileOrDirectory{
						&cwlcore.File{
							Path: "s3://bucket/x.bai", Location: "file:///tmp/x.bai", Basename: "x.bai",
						},
					},
				},
			},
		},
		{
			name: "inconsistent listing entry",
			outputs: map[string]any{
				"dir": &cwlcore.Directory{
					Path: "s3://bucket/d", Location: "s3://bucket/d", Basename: "d",
					Listing: []cwlcore.FileOrDirectory{
						&cwlcore.File{
							Path: "s3://bucket/d/f.txt", Location: "file:///tmp/f.txt", Basename: "f.txt",
						},
					},
				},
			},
		},
		{
			name: "nested in array",
			outputs: map[string]any{
				"arr": []any{
					&cwlcore.File{
						Path: "s3://bucket/a.txt", Location: "file:///tmp/a.txt", Basename: "a.txt",
					},
				},
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
