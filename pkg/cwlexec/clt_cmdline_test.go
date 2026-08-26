package cwlexec

import (
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// cltBaseCommandCases is the table TestBuildCommandLineBaseCommand runs.
var cltBaseCommandCases = []cltCase{
	{
		name: "single string baseCommand",
		tool: &cwlcore.CommandLineTool{BaseCommand: []string{cltEcho}},
		want: []string{cltEcho},
	},
	{
		name: "list baseCommand keeps its order",
		tool: &cwlcore.CommandLineTool{BaseCommand: []string{"python", "script.py", "--go"}},
		want: []string{"python", "script.py", "--go"},
	},
	{
		name: "no baseCommand leaves the bindings as the whole command line",
		tool: &cwlcore.CommandLineTool{
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentString("cat"),
			},
		},
		want: []string{"cat"},
	},
	{
		name: "baseCommand precedes every binding, including a negative position",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs:      []cwlcore.CommandInputParameter{cltInput("x", cltString, cltAt(-99))},
		},
		inputs: map[string]any{"x": "v"},
		want:   []string{"t", "v"},
	},
	{
		name: "redirections are not argv",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Stdin:       "in.txt",
			Stdout:      "out.txt",
			Stderr:      "err.txt",
		},
		want: []string{"t"},
	},
}

func TestBuildCommandLineBaseCommand(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltBaseCommandCases)
}

// cltOrderingCases is the table TestBuildCommandLineOrdering runs.
var cltOrderingCases = []cltCase{
	{
		name: "positions order the bindings, not document order",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("third", cltString, cltAt(3)),
				cltInput("first", cltString, cltAt(1)),
				cltInput("second", cltString, cltAt(2)),
			},
		},
		inputs: map[string]any{"third": "3", "first": "1", "second": "2"},
		want:   []string{"t", "1", "2", "3"},
	},
	{
		name: "equal positions break the tie by parameter name",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltBeta, cltString, cltAt(0)),
				cltInput(cltAlpha, cltString, cltAt(0)),
			},
		},
		inputs: map[string]any{cltBeta: "B", cltAlpha: "A"},
		want:   []string{"t", "A", "B"},
	},
	{
		name: "a negative position sorts before the default of zero",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("zero", cltString, &cwlcore.CommandLineBinding{}),
				cltInput("under", cltString, cltAt(-1)),
			},
		},
		inputs: map[string]any{"zero": "0", "under": "-1"},
		want:   []string{"t", "-1", "0"},
	},
	{
		name: "an unbound parameter contributes nothing",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("bound", cltString, cltAt(0)),
				cltInput("unbound", cltString, nil),
			},
		},
		inputs: map[string]any{"bound": "b", "unbound": "u"},
		want:   []string{"t", "b"},
	},
	{
		name: "an arguments entry sorts before an input sharing its position",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs:      []cwlcore.CommandInputParameter{cltInput("a", cltString, cltAt(0))},
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentString("arg"),
			},
		},
		inputs: map[string]any{"a": "A"},
		want:   []string{"t", "arg", "A"},
	},
	{
		name: "arguments at equal positions keep their document order",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Arguments: []cwlcore.CommandLineArgument{
				cwlcore.NewCommandLineArgumentString("zzz"),
				cwlcore.NewCommandLineArgumentString("aaa"),
			},
		},
		want: []string{"t", "zzz", "aaa"},
	},
	{
		name: "an expression position is evaluated before sorting",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("late", cltString, &cwlcore.CommandLineBinding{
					Position: cwlcore.NewExprLongExpression("$(inputs.slot)"),
				}),
				cltInput("early", cltString, cltAt(1)),
				cltInput("slot", cltInt, nil),
			},
		},
		inputs: map[string]any{"late": "L", "early": "E", "slot": int64(9)},
		want:   []string{"t", "E", "L"},
	},
	{
		name: "an expression position sees self as the bound value",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("moved", cltString, &cwlcore.CommandLineBinding{
					Position: cwlcore.NewExprLongExpression("${return self.length;}"),
				}),
				cltInput("fixed", cltString, cltAt(2)),
			},
		},
		inputs: map[string]any{"moved": "abcd", "fixed": "F"},
		want:   []string{"t", "F", "abcd"},
	},
	{
		name: "a null position expression falls back to the schema default",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("nulled", cltString, &cwlcore.CommandLineBinding{
					Position: cwlcore.NewExprLongExpression("${return null;}"),
				}),
				cltInput("after", cltString, cltAt(1)),
			},
		},
		inputs: map[string]any{"nulled": "N", "after": "A"},
		want:   []string{"t", "N", "A"},
	},
}

func TestBuildCommandLineOrdering(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltOrderingCases)
}

// cltScalarTypesCases is the table TestBuildCommandLineScalarTypes runs.
var cltScalarTypesCases = []cltCase{
	{
		name: "a null value adds nothing, not even the prefix",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("maybe", cwlcore.NewUnionType([]cwlcore.TypeRef{cltNull, cltString}),
					cltPrefixed(0, "--maybe")),
			},
		},
		inputs: map[string]any{"maybe": nil},
		want:   []string{"t"},
	},
	{
		name: "an absent input is null too",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("absent", cltString, cltPrefixed(0, "--absent")),
			},
		},
		want: []string{"t"},
	},
	{
		name: "a true boolean adds its prefix alone",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltToggle, cltBool, cltPrefixed(0, "--flag")),
			},
		},
		inputs: map[string]any{cltToggle: true},
		want:   []string{"t", "--flag"},
	},
	{
		name: "a false boolean adds nothing",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltToggle, cltBool, cltPrefixed(0, "--flag")),
			},
		},
		inputs: map[string]any{cltToggle: false},
		want:   []string{"t"},
	},
	{
		// The shape conformance test booleanflags_cl_noinputbinding pins: a boolean bound
		// with an empty inputBinding contributes nothing however it is set. See [renderTrue].
		name: "a true boolean with no prefix adds nothing either",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltToggle, cltBool, &cwlcore.CommandLineBinding{}),
			},
		},
		inputs: map[string]any{cltToggle: true},
		want:   []string{"t"},
	},
	{
		name: "a File renders as its path, not its location",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("f", cltFile, cltPrefixed(0, "-f")),
			},
		},
		inputs: map[string]any{
			"f": &cwlcore.File{Location: "file:///elsewhere/a.txt", Path: "/staged/a.txt"},
		},
		want: []string{"t", "-f", "/staged/a.txt"},
	},
	{
		name: "a Directory renders as its path",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("d", cltDir, cltPrefixed(0, "-d")),
			},
		},
		inputs: map[string]any{"d": &cwlcore.Directory{Path: "/staged/dir"}},
		want:   []string{"t", "-d", "/staged/dir"},
	},
	{
		name: "a File in decoded map form renders as its path too",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("f", cltFile, cltAt(0)),
			},
		},
		inputs: map[string]any{
			"f": map[string]any{fileClassField: cwlcore.ClassFile, filePathField: "/staged/b.txt"},
		},
		want: []string{"t", "/staged/b.txt"},
	},
	{
		name: "numbers render in decimal, floats the way the reference implementation writes them",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("i", cltInt, cltAt(1)),
				cltInput("f", cltAny, cltAt(2)),
				cltInput("w", cltAny, cltAt(3)),
			},
		},
		inputs: map[string]any{"i": int64(42), "f": 3.5, "w": 4.0},
		want:   []string{"t", "42", "3.5", "4.0"},
	},
	{
		name: "a record adds its prefix and then its bound fields, ordered by field name",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltRec, cwlcore.NewRecordType(&cwlcore.RecordSchema{
					Fields: []cwlcore.RecordField{
						{Name: cltToolID + cltRec + "/beta", Type: cltString, InputBinding: cltPrefixed(0, "-b")},
						{Name: cltToolID + cltRec + "/alpha", Type: cltString, InputBinding: cltPrefixed(0, "-a")},
						{Name: cltToolID + cltRec + "/hidden", Type: cltString},
					},
				}), cltPrefixed(0, "-r")),
			},
		},
		inputs: map[string]any{
			cltRec: map[string]any{cltAlpha: "A", cltBeta: "B", "hidden": "H"},
		},
		want: []string{"t", "-r", "-a", "A", "-b", "B"},
	},
	{
		name: "a record field's own position orders it against its siblings",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltRec, cwlcore.NewRecordType(&cwlcore.RecordSchema{
					Fields: []cwlcore.RecordField{
						{Name: cltToolID + cltRec + "/alpha", Type: cltString, InputBinding: cltAt(5)},
						{Name: cltToolID + cltRec + "/beta", Type: cltString, InputBinding: cltAt(1)},
					},
				}), cltAt(0)),
			},
		},
		inputs: map[string]any{cltRec: map[string]any{cltAlpha: "A", cltBeta: "B"}},
		want:   []string{"t", "B", "A"},
	},
}

func TestBuildCommandLineScalarTypes(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltScalarTypesCases)
}

// cltPrefixAndSeparateCases is the table TestBuildCommandLinePrefixAndSeparate runs.
var cltPrefixAndSeparateCases = []cltCase{
	{
		name: "an absent separate defaults to two arguments",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, cltPrefixed(0, cltOpt)),
			},
		},
		inputs: map[string]any{"x": "v"},
		want:   []string{cltOpt, "v"},
	},
	{
		name: "separate true is the same as absent",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Prefix: cltOpt, Separate: cwlcore.NewOptBool(true),
				}),
			},
		},
		inputs: map[string]any{"x": "v"},
		want:   []string{cltOpt, "v"},
	},
	{
		name: "separate false concatenates the prefix and the value",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, &cwlcore.CommandLineBinding{
					Prefix: "--opt=", Separate: cwlcore.NewOptBool(false),
				}),
			},
		},
		inputs: map[string]any{"x": "v"},
		want:   []string{"--opt=v"},
	},
	{
		name: "no prefix emits the value alone",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, cltAt(0)),
			},
		},
		inputs: map[string]any{"x": "v"},
		want:   []string{"v"},
	},
}

func TestBuildCommandLinePrefixAndSeparate(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltPrefixAndSeparateCases)
}
