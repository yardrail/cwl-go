package cwlcli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// The fixtures the tests below share, hoisted so that no literal repeats.
const (
	twoLines   = "a\nb"
	threeLines = "a\nb\nc"
)

// errPlain is an error that is not a salad.Error, for the fallback path.
var errPlain = errors.New("plain")

func TestExplainKeepsTheErrorTree(t *testing.T) {
	t.Parallel()

	loc := salad.SourceLine{
		File:  "file:///tmp/tool.cwl",
		Start: salad.Position{Line: 6, Column: 11, Offset: 0},
		End:   salad.Position{Line: 6, Column: 11, Offset: 0},
	}
	err := salad.Group(salad.SourceLine{}, "outer", salad.Errorf(loc, "inner detail"))

	got := Explain(err)
	if !strings.Contains(got, "inner detail") {
		t.Errorf("Explain = %q, want the child error kept", got)
	}

	if !strings.Contains(got, "tool.cwl:6:11") {
		t.Errorf("Explain = %q, want the source line named", got)
	}

	if strings.Count(got, "\n") == 0 {
		t.Errorf("Explain = %q, want the tree kept over several lines", got)
	}
}

func TestExplainUnwrapsThroughWrapping(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("while loading: %w", salad.Errorf(salad.SourceLine{}, "root cause"))

	if got := Explain(wrapped); !strings.Contains(got, "root cause") {
		t.Errorf("Explain = %q, want the wrapped salad error rendered", got)
	}
}

func TestExplainFallsBackToPlainErrors(t *testing.T) {
	t.Parallel()

	if got := Explain(errPlain); got != "plain" {
		t.Errorf("Explain = %q, want plain", got)
	}

	if got := Explain(nil); got != "" {
		t.Errorf("Explain(nil) = %q, want empty", got)
	}
}

func TestLimitLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		want        string
		limit       int
		wantOmitted int
	}{
		{name: "under the limit", input: twoLines, limit: 5, want: twoLines, wantOmitted: 0},
		{name: "at the limit", input: twoLines, limit: 2, want: twoLines, wantOmitted: 0},
		{name: "over the limit", input: threeLines, limit: 2, want: twoLines, wantOmitted: 1},
		{name: "no limit", input: threeLines, limit: 0, want: threeLines, wantOmitted: 0},
		{name: "empty", input: "", limit: 2, want: "", wantOmitted: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, omitted := LimitLines(tc.input, tc.limit)
			if got != tc.want || omitted != tc.wantOmitted {
				t.Errorf("LimitLines = (%q, %d), want (%q, %d)", got, omitted, tc.want, tc.wantOmitted)
			}
		})
	}
}

func TestIndent(t *testing.T) {
	t.Parallel()

	if got := Indent("a\n\nb", "  "); got != "  a\n\n  b" {
		t.Errorf("Indent = %q, want %q", got, "  a\n\n  b")
	}

	if got := Indent("", "  "); got != "" {
		t.Errorf("Indent(\"\") = %q, want empty", got)
	}
}
