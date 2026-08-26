package salad

import (
	"errors"
	"strings"
	"testing"
)

func TestSourceLineString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		loc  SourceLine
	}{
		{name: "zero", loc: SourceLine{}, want: ""},
		{name: "file only", loc: SourceLine{File: testFile}, want: testFile},
		{
			name: "file and position",
			loc:  SourceLine{File: testFile, Start: Position{Line: 3, Column: 5}},
			want: testFile + ":3:5",
		},
		{
			name: "position only",
			loc:  SourceLine{Start: Position{Line: 7, Column: 1}},
			want: "7:1",
		},
		{
			name: "column zero still prints",
			loc:  SourceLine{File: testFile, Start: Position{Line: 2}},
			want: testFile + ":2:0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.loc.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSourceLineIsZero(t *testing.T) {
	t.Parallel()

	if !(SourceLine{}).IsZero() {
		t.Error("zero SourceLine should report IsZero")
	}

	if (SourceLine{File: testFile}).IsZero() {
		t.Error("SourceLine with a file should not report IsZero")
	}

	if (SourceLine{Start: Position{Line: 1}}).IsZero() {
		t.Error("SourceLine with a position should not report IsZero")
	}
}

func TestErrorSummary(t *testing.T) {
	t.Parallel()

	loc := SourceLine{File: "wf.cwl", Start: Position{Line: 4, Column: 3}}

	tests := []struct {
		err  *Error
		name string
		want string
	}{
		{
			name: "leaf with location",
			err:  Errorf(loc, "expected %s, got %s", nameString, nameInt),
			want: "wf.cwl:4:3: expected string, got int",
		},
		{
			name: "leaf without location",
			err:  Errorf(SourceLine{}, "no location here"),
			want: "no location here",
		},
		{
			name: "warning is marked",
			err:  Warnf(loc, "unrecognized field `colour`"),
			want: "wf.cwl:4:3: warning: unrecognized field `colour`",
		},
		{
			name: "grouping node reports its first leaf",
			err:  Group(SourceLine{}, "", Group(loc, "", Errorf(loc, "the real problem"))),
			want: "wf.cwl:4:3: the real problem",
		},
		{
			name: "empty tree still says something",
			err:  &Error{},
			want: emptyErrorMsg,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestErrorGroupDropsNilChildren(t *testing.T) {
	t.Parallel()

	g := Group(SourceLine{}, "context", nil, Errorf(SourceLine{}, "real"), nil)
	if len(g.Children) != 1 {
		t.Fatalf("Children = %d, want 1", len(g.Children))
	}

	if len(g.Unwrap()) != 1 {
		t.Fatalf("Unwrap() = %d, want 1", len(g.Unwrap()))
	}
}

func TestErrorUnwrapTraversal(t *testing.T) {
	t.Parallel()

	deep := Errorf(SourceLine{File: "deep.yml"}, "deepest")
	root := Group(SourceLine{}, "top", Group(SourceLine{}, "middle", deep))

	var target *Error
	if !errors.As(error(root), &target) {
		t.Fatal("errors.As should recover the root")
	}

	if target != root {
		t.Errorf("errors.As recovered %v, want the root", target)
	}

	if !errors.Is(error(root), error(deep)) {
		t.Error("errors.Is should find a grandchild through Unwrap() []error")
	}

	if (&Error{}).Unwrap() != nil {
		t.Error("a childless error should Unwrap to nil")
	}
}

func TestErrorLeaves(t *testing.T) {
	t.Parallel()

	a := Errorf(SourceLine{}, "a")
	b := Errorf(SourceLine{}, "b")
	c := Errorf(SourceLine{}, "c")
	root := Group(SourceLine{}, "root", Group(SourceLine{}, "branch", a, b), c)

	leaves := root.Leaves()
	want := []*Error{a, b, c}

	if len(leaves) != len(want) {
		t.Fatalf("Leaves() returned %d errors, want %d", len(leaves), len(want))
	}

	for i := range want {
		if leaves[i] != want[i] {
			t.Errorf("Leaves()[%d] = %q, want %q", i, leaves[i].Msg, want[i].Msg)
		}
	}
}

func TestErrorLeavesSkipsMessagelessNodes(t *testing.T) {
	t.Parallel()

	if got := len(Group(SourceLine{}, "").Leaves()); got != 0 {
		t.Errorf("a messageless childless error has %d leaves, want 0", got)
	}
}

func TestErrorPretty(t *testing.T) {
	t.Parallel()

	src := "wf.cwl"
	root := Group(
		SourceLine{File: src, Start: Position{Line: 1, Column: 1}},
		"document is not valid",
		Group(
			SourceLine{File: src, Start: Position{Line: 4, Column: 3}},
			"tried CommandLineTool",
			Errorf(SourceLine{File: src, Start: Position{Line: 5, Column: 7}}, "the `inputs` field is required"),
		),
		Group(
			SourceLine{},
			"",
			Warnf(SourceLine{File: src, Start: Position{Line: 9, Column: 3}}, "field `colour` is not recognized"),
		),
	)

	want := strings.Join([]string{
		"wf.cwl:1:1: document is not valid",
		"  wf.cwl:4:3: tried CommandLineTool",
		"    wf.cwl:5:7: the `inputs` field is required",
		"  wf.cwl:9:3: warning: field `colour` is not recognized",
	}, "\n")

	if got := root.Pretty(); got != want {
		t.Errorf("Pretty() =\n%s\n\nwant\n%s", got, want)
	}
}

func TestErrorPrettySingleLeaf(t *testing.T) {
	t.Parallel()

	err := Errorf(SourceLine{Start: Position{Line: 2, Column: 4}}, "bad value")
	if got, want := err.Pretty(), "2:4: bad value"; got != want {
		t.Errorf("Pretty() = %q, want %q", got, want)
	}
}

func TestErrorNilReceiverIsSafe(t *testing.T) {
	t.Parallel()

	var err *Error
	if got := err.Error(); got != "<nil>" {
		t.Errorf("nil Error() = %q, want %q", got, "<nil>")
	}

	if got := err.Pretty(); got != "" {
		t.Errorf("nil Pretty() = %q, want empty", got)
	}

	if got := err.Leaves(); len(got) != 0 {
		t.Errorf("nil Leaves() = %v, want empty", got)
	}

	if got := err.Unwrap(); got != nil {
		t.Errorf("nil Unwrap() = %v, want nil", got)
	}
}
