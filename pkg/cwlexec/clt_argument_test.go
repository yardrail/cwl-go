package cwlexec

import (
	"errors"
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// cltArgBinding is the full CommandLineBinding form of an arguments entry: a prefix, an explicit
// position, and the valueFrom the specification requires there.
var cltArgBinding = &cwlcore.CommandLineBinding{
	Prefix:    "-B",
	Position:  cwlcore.NewExprLong(2),
	ValueFrom: "argvalue",
}

// cltArgumentsCases is the table TestBuildCommandLineArguments runs.
var cltArgumentsCases = []cltCase{
	{
		name: "all three argument forms interleave with inputs by position",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, cltAt(1)),
				cltInput("y", cltString, cltAt(3)),
			},
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentBinding(cltArgBinding),
				cwlcore.NewCommandLineArgumentExpression("$(inputs.x)-expr"),
				cwlcore.NewCommandLineArgumentString("literal"),
			},
		},
		inputs: map[string]any{"x": "X", "y": "Y"},
		// The expression and the literal both default to position 0 and keep their
		// document order; the binding declares position 2, between the two inputs.
		want: []string{"t", "X-expr", "literal", "X", "-B", "argvalue", "Y"},
	},
	{
		name: "an argument expression preserving its type renders as that type",
		tool: &cwlcore.CommandLineTool{
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentExpression("$(inputs.n)"),
			},
			Inputs: []cwlcore.CommandInputParameter{cltInput("n", cltInt, nil)},
		},
		inputs: map[string]any{"n": int64(7)},
		want:   []string{"7"},
	},
	{
		name: "an argument binding whose valueFrom yields a list emits every element",
		tool: &cwlcore.CommandLineTool{
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentBinding(&cwlcore.CommandLineBinding{
					Prefix:    "-l",
					ValueFrom: "${return ['a', 'b'];}",
				}),
			},
		},
		want: []string{"-l", "a", "b"},
	},
	{
		name: "an argument binding whose valueFrom yields a list joins it with itemSeparator",
		tool: &cwlcore.CommandLineTool{
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentBinding(&cwlcore.CommandLineBinding{
					Prefix:        "-l",
					ItemSeparator: ",",
					ValueFrom:     "${return ['a', 'b'];}",
				}),
			},
		},
		want: []string{"-l", "a,b"},
	},
	{
		name: "an argument expression yielding null adds nothing",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentExpression("${return null;}"),
			},
		},
		want: []string{"t"},
	},
}

func TestBuildCommandLineArguments(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltArgumentsCases)
}

// cltValueFromCases is the table TestBuildCommandLineValueFrom runs.
var cltValueFromCases = []cltCase{
	{
		name: "valueFrom replaces the value, with self bound to the input",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Prefix:    "-x",
					ValueFrom: "$(self)!",
				}),
			},
		},
		inputs: map[string]any{"x": cltRaw},
		want:   []string{"-x", "raw!"},
	},
	{
		name: "valueFrom sees self as a File object",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("f", cltFile, &cwlcore.CommandLineBinding{ValueFrom: "$(self.basename)"}),
			},
		},
		inputs: map[string]any{
			"f": map[string]any{fileClassField: cwlcore.ClassFile, filePathField: "/s/a.txt", "basename": "a.txt"},
		},
		want: []string{"a.txt"},
	},
	{
		name: "valueFrom also sees the whole input object and the runtime context",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{ValueFrom: "$(inputs.other)"}),
				cltInput("other", cltString, nil),
			},
		},
		inputs: map[string]any{"x": "ignored", "other": "seen"},
		want:   []string{"seen"},
	},
	{
		name: "a constant valueFrom is used verbatim",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{ValueFrom: "constant"}),
			},
		},
		inputs: map[string]any{"x": cltRaw},
		want:   []string{"constant"},
	},
	{
		name: "valueFrom is not evaluated at all when the input is null",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{ValueFrom: "${throw 'boom';}"}),
			},
		},
		want: []string{"t"},
	},
	{
		name: "valueFrom is terminal: the declared array structure is not walked",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltStringArray, &cwlcore.CommandLineBinding{
					ValueFrom: "${return self.length;}",
				}),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b", "c"}},
		want:   []string{"3"},
	},
	{
		name: "valueFrom yielding null adds nothing",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Prefix:    "-x",
					ValueFrom: "${return null;}",
				}),
			},
		},
		inputs: map[string]any{"x": cltRaw},
		want:   []string{"t"},
	},
	{
		name: "valueFrom yielding an object adds the prefix only",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Prefix:    "-x",
					ValueFrom: "${return {a: 1};}",
				}),
			},
		},
		inputs: map[string]any{"x": cltRaw},
		want:   []string{"-x"},
	},
	{
		name: "valueFrom yielding false adds nothing",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Prefix:    "-x",
					ValueFrom: "${return false;}",
				}),
			},
		},
		inputs: map[string]any{"x": cltRaw},
		want:   []string{"t"},
	},
	{
		name: "valueFrom yielding an empty list adds nothing",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Prefix:    "-x",
					ValueFrom: "${return [];}",
				}),
			},
		},
		inputs: map[string]any{"x": cltRaw},
		want:   []string{"t"},
	},
}

func TestBuildCommandLineValueFrom(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltValueFromCases)
}

// cltErrCase is one row of the failure table: a tool and inputs that must not build, and the sentinel
// the failure has to wrap.

// cltErrorsCases is the table TestBuildCommandLineErrors runs.
var cltErrorsCases = []cltErrCase{
	{
		name: "separate false without a prefix says nothing",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Separate: cwlcore.NewOptBool(false),
				}),
			},
		},
		inputs:  map[string]any{"x": "v"},
		wantErr: ErrBindingPrefix,
	},
	{
		name: "a File with no path cannot be named",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{cltInput("f", cltFile, cltAt(0))},
		},
		inputs:  map[string]any{"f": &cwlcore.File{Location: "file:///a.txt"}},
		wantErr: ErrBindingValue,
	},
	{
		name: "a position expression that does not yield an integer",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Position: cwlcore.NewExprLongExpression("$(self)"),
				}),
			},
		},
		inputs:  map[string]any{"x": "not a number"},
		wantErr: ErrBindingPosition,
	},
	{
		name: "a position expression that fails to evaluate",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Position: cwlcore.NewExprLongExpression("$(inputs.nope)"),
				}),
			},
		},
		inputs:  map[string]any{"x": "v"},
		wantErr: cwlcore.ErrExpressionEval,
	},
	{
		name: "a valueFrom that fails to evaluate",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{ValueFrom: "${throw 'boom';}"}),
			},
		},
		inputs:  map[string]any{"x": "v"},
		wantErr: cwlcore.ErrExpressionEval,
	},
	{
		name: "an arguments binding with no valueFrom",
		tool: &cwlcore.CommandLineTool{
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentBinding(cltPrefixed(0, "-x")),
			},
		},
		wantErr: ErrArgumentValueFrom,
	},
	{
		name: "an arguments entry that decoded to nothing at all",
		tool: &cwlcore.CommandLineTool{
			Arguments: []cwlcore.CommandLineArgument{{}},
		},
		wantErr: ErrArgumentValueFrom,
	},
	{
		name: "an arguments binding with a bad position expression",
		tool: &cwlcore.CommandLineTool{
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentBinding(&cwlcore.CommandLineBinding{
					Position:  cwlcore.NewExprLongExpression("${return 'here';}"),
					ValueFrom: "v",
				}),
			},
		},
		wantErr: ErrBindingPosition,
	},
	{
		name: "an arguments valueFrom that fails to evaluate",
		tool: &cwlcore.CommandLineTool{
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentExpression("${throw 'boom';}"),
			},
		},
		wantErr: cwlcore.ErrExpressionEval,
	},
	{
		name: "a value with no command line form",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltStringArray, &cwlcore.CommandLineBinding{ItemSeparator: ","}),
			},
		},
		inputs:  map[string]any{"x": []any{map[string]any{"k": "v"}}},
		wantErr: ErrBindingValue,
	},
	{
		name: "a valueFrom list holding a value with no command line form",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					ValueFrom: "${return [{a: 1}];}",
				}),
			},
		},
		inputs:  map[string]any{"x": "v"},
		wantErr: ErrBindingValue,
	},
}

func TestBuildCommandLineErrors(t *testing.T) {
	t.Parallel()

	runCltErrCases(t, cltErrorsCases)
}

func TestBuildCommandLineRejectsNilTool(t *testing.T) {
	t.Parallel()

	_, err := BuildCommandLine(nil, nil, nil, nil, cwlcore.RuntimeContext{})
	if !errors.Is(err, ErrWrongProcessClass) {
		t.Fatalf("BuildCommandLine(nil): error %v does not wrap %v", err, ErrWrongProcessClass)
	}
}

func TestBuildCommandLineWithoutJavaScript(t *testing.T) {
	t.Parallel()

	tool := &cwlcore.CommandLineTool{
		BaseCommand: []string{"t"},
		Inputs: []cwlcore.CommandInputParameter{
			cltInput("x", cltString, &cwlcore.CommandLineBinding{ValueFrom: "$(self)/sub"}),
		},
	}

	// A nil evaluator is the parameter-references-only configuration every conforming
	// implementation must support, and it is what a hand-built call passes.
	line, err := BuildCommandLine(tool, map[string]any{"x": "dir"}, nil, nil, cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("BuildCommandLine: unexpected error: %v", err)
	}

	if want := []string{"t", "dir/sub"}; !slices.Equal(line.Argv(), want) {
		t.Errorf("argv = %q, want %q", line.Argv(), want)
	}
}

func TestBuildCommandLineRuntimeContext(t *testing.T) {
	t.Parallel()

	tool := &cwlcore.CommandLineTool{
		Inputs: []cwlcore.CommandInputParameter{
			cltInput("x", cltString, &cwlcore.CommandLineBinding{ValueFrom: "$(runtime.outdir)/out"}),
		},
	}

	runtime := cwlcore.RuntimeContext{Outdir: "/work/out"}

	line, err := BuildCommandLine(tool, map[string]any{"x": "v"}, cltEval(), nil, runtime)
	if err != nil {
		t.Fatalf("BuildCommandLine: unexpected error: %v", err)
	}

	if want := []string{"/work/out/out"}; !slices.Equal(line.Argv(), want) {
		t.Errorf("argv = %q, want %q", line.Argv(), want)
	}
}
