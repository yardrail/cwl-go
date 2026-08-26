package cwlexec

import (
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// cltArraysCases is the table TestBuildCommandLineArrays runs.
var cltArraysCases = []cltCase{
	{
		name: "an empty array adds nothing, not even the prefix",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltStringArray, cltPrefixed(0, "-x")),
			},
		},
		inputs: map[string]any{"xs": make([]any, 0)},
		want:   []string{"t"},
	},
	{
		name: "an empty array with itemSeparator adds nothing either",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltStringArray, &cwlcore.CommandLineBinding{
					Prefix: "-x", ItemSeparator: ",",
				}),
			},
		},
		inputs: map[string]any{"xs": make([]any, 0)},
		want:   []string{"t"},
	},
	{
		name: "an array binding adds its prefix once and then every element",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltStringArray, cltPrefixed(0, "-x")),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b", "c"}},
		want:   []string{"t", "-x", "a", "b", "c"},
	},
	{
		name: "itemSeparator joins the elements into one argument",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltStringArray, &cwlcore.CommandLineBinding{
					Prefix: "-x", ItemSeparator: ",",
				}),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b"}},
		want:   []string{"t", "-x", "a,b"},
	},
	{
		name: "itemSeparator with separate false concatenates the whole joined value",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltStringArray, &cwlcore.CommandLineBinding{
					Prefix: "-x=", ItemSeparator: ":", Separate: cwlcore.NewOptBool(false),
				}),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b"}},
		want:   []string{"-x=a:b"},
	},
	{
		name: "a binding on the items repeats its prefix once per element",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltItemBound(cltPrefixed(0, "-i")), nil),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b"}},
		want:   []string{"t", "-i", "a", "-i", "b"},
	},
	{
		name: "the array's binding and the items' binding are different bindings",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltItemBound(cltPrefixed(0, "-i")), cltPrefixed(0, "-x")),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b"}},
		want:   []string{"t", "-x", "-i", "a", "-i", "b"},
	},
	{
		name: "an item binding may concatenate its own prefix",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltItemBound(&cwlcore.CommandLineBinding{
					Prefix: "-D", Separate: cwlcore.NewOptBool(false),
				}), nil),
			},
		},
		inputs: map[string]any{"xs": []any{"a=1", "b=2"}},
		want:   []string{"-Da=1", "-Db=2"},
	},
	{
		name: "elements keep their index order whatever their shared position",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltItemBound(cltAt(4)), cltAt(0)),
				cltInput("y", cltString, cltAt(1)),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b", "c"}, "y": "Y"},
		want:   []string{"a", "b", "c", "Y"},
	},
	{
		name: "an array with no binding anywhere contributes nothing",
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltStringArray, nil),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b"}},
		want:   []string{"t"},
	},
	{
		name: "itemSeparator claims the elements, so no item binding is synthesized",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltStringArray, &cwlcore.CommandLineBinding{ItemSeparator: "+"}),
			},
		},
		inputs: map[string]any{"xs": []any{"a", "b"}},
		want:   []string{"a+b"},
	},
	{
		name: "an array of Files renders each path",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("fs", cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: cltFile}),
					cltPrefixed(0, "-f")),
			},
		},
		inputs: map[string]any{
			"fs": []any{&cwlcore.File{Path: "/a"}, &cwlcore.File{Path: "/b"}},
		},
		want: []string{"-f", "/a", "/b"},
	},
	{
		name: "a nested array binds through both levels",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xss", cwlcore.NewArrayType(&cwlcore.ArraySchema{
					Items: cltItemBound(cltPrefixed(0, "-i")),
				}), cltPrefixed(0, "-x")),
			},
		},
		inputs: map[string]any{"xss": []any{[]any{"a", "b"}, []any{"c"}}},
		want:   []string{"-x", "-i", "a", "-i", "b", "-i", "c"},
	},
	{
		name: "an array inside a record is bound through the field's own binding",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltRec, cwlcore.NewRecordType(&cwlcore.RecordSchema{
					Fields: []cwlcore.RecordField{{
						Name:         cltToolID + cltRec + "/items",
						Type:         cltStringArray,
						InputBinding: &cwlcore.CommandLineBinding{Prefix: "-l", ItemSeparator: ";"},
					}},
				}), cltPrefixed(0, "-r")),
			},
		},
		inputs: map[string]any{cltRec: map[string]any{"items": []any{"p", "q"}}},
		want:   []string{"-r", "-l", "p;q"},
	},
}

func TestBuildCommandLineArrays(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltArraysCases)
}

// cltSchemaLevelBindingsCases is the table TestBuildCommandLineSchemaLevelBindings runs.
var cltSchemaLevelBindingsCases = []cltCase{
	{
		name: "a record schema's own binding is a level of its own",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltRec, cwlcore.NewRecordType(&cwlcore.RecordSchema{
					InputBinding: cltPrefixed(0, "-inner"),
					Fields: []cwlcore.RecordField{{
						Name: cltToolID + cltRec + "/f", Type: cltString, InputBinding: cltPrefixed(0, "-f"),
					}},
				}), cltPrefixed(0, "-outer")),
			},
		},
		inputs: map[string]any{cltRec: map[string]any{"f": "V"}},
		// Both schema levels sit at position 0 with the same tie-break name, so their
		// keys are wholly equal and the stable sort keeps the outer one first.
		want: []string{"-outer", "-inner", "-f", "V"},
	},
	{
		name: "an enum schema's own binding binds the symbol again",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("mode", cwlcore.NewEnumType(&cwlcore.EnumSchema{
					Symbols:      []string{cltToolID + "mode/fast"},
					InputBinding: cltPrefixed(1, "-e"),
				}), cltPrefixed(0, "-m")),
			},
		},
		inputs: map[string]any{"mode": cltFast},
		want:   []string{"-m", cltFast, "-e", cltFast},
	},
	{
		name: "an enum with no binding of its own is bound once",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("mode", cwlcore.NewEnumType(&cwlcore.EnumSchema{
					Symbols: []string{cltToolID + "mode/fast"},
				}), cltPrefixed(0, "-m")),
			},
		},
		inputs: map[string]any{"mode": cltFast},
		want:   []string{"-m", cltFast},
	},
	{
		name: "a union resolves to the member the value's shape describes",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltNull, cltString, cltItemBound(cltPrefixed(0, "-i")),
				}), cltPrefixed(0, "-e")),
			},
		},
		inputs: map[string]any{cltEither: []any{"a", "b"}},
		want:   []string{"-e", "-i", "a", "-i", "b"},
	},
	{
		name: "a union whose scalar member matches walks no further",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{cltNull, cltString}),
					cltPrefixed(0, "-e")),
			},
		},
		inputs: map[string]any{cltEither: "s"},
		want:   []string{"-e", "s"},
	},
	{
		name: "a union resolving to a record walks its fields",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltNull,
					cwlcore.NewRecordType(&cwlcore.RecordSchema{
						Fields: []cwlcore.RecordField{{
							Name: cltToolID + cltEither + "/f", Type: cltString,
							InputBinding: cltPrefixed(0, "-f"),
						}},
					}),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{"f": "V"}},
		want:   []string{"-f", "V"},
	},
	{
		name: "a union resolving to an enum uses the enum's own binding",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltNull,
					cwlcore.NewEnumType(&cwlcore.EnumSchema{InputBinding: cltPrefixed(0, "-e")}),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: cltFast},
		want:   []string{"-e", cltFast},
	},
	{
		name: "a union of records picks the member whose field set the value fits",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltUnionRecord(cltAlpha, "-a"), cltUnionRecord(cltBeta, "-b"),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltBeta: "V"}},
		want:   []string{"-b", "V"},
	},
	{
		name: "a union of records tells two members with the same fields apart by enum symbol",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltTaggedRecord(cltFast, "-f"), cltTaggedRecord(cltSolo, "-s"),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltSym: cltSolo}},
		want:   []string{"-s", cltSolo},
	},
	{
		name: "an enum symbol written as a resolved identifier still matches its short spelling",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltTaggedRecord(cltToolID+cltFast, "-f"), cltTaggedRecord(cltSolo, "-s"),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltSym: cltFast}},
		want:   []string{"-f", cltFast},
	},
	{
		name: "a record member missing a field the value carries is ruled out",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltUnionRecord(cltAlpha, "-a"),
					cwlcore.NewRecordType(&cwlcore.RecordSchema{Fields: []cwlcore.RecordField{
						cltRecordField(cltAlpha, cltString, cltPrefixed(0, "-A")),
						cltRecordField(cltBeta, cltString, cltPrefixed(1, "-B")),
					}}),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltAlpha: "1", cltBeta: "2"}},
		want:   []string{"-A", "1", "-B", "2"},
	},
	{
		name: "a record member whose required field the value omits is ruled out",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cwlcore.NewRecordType(&cwlcore.RecordSchema{Fields: []cwlcore.RecordField{
						cltRecordField(cltAlpha, cltString, cltPrefixed(0, "-A")),
						cltRecordField(cltBeta, cltString, cltPrefixed(1, "-B")),
					}}),
					cwlcore.NewRecordType(&cwlcore.RecordSchema{Fields: []cwlcore.RecordField{
						cltRecordField(cltAlpha, cltString, cltPrefixed(0, "-a")),
						cltRecordField(cltBeta,
							cwlcore.NewUnionType([]cwlcore.TypeRef{cltNull, cltString}),
							cltPrefixed(1, "-b")),
					}}),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltAlpha: "1"}},
		want:   []string{"-a", "1"},
	},
	{
		name: "a record member with no schema accepts nothing and the next one is tried",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cwlcore.NewRecordType(nil), cltUnionRecord(cltAlpha, "-a"),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltAlpha: "V"}},
		want:   []string{"-a", "V"},
	},
	{
		name: "an enum member is skipped for a value that is not a symbol",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cwlcore.NewEnumType(&cwlcore.EnumSchema{
						Symbols: []string{cltFast}, InputBinding: cltPrefixed(0, "-e"),
					}),
					cltUnionRecord(cltAlpha, "-a"),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltAlpha: "V"}},
		want:   []string{"-a", "V"},
	},
	{
		name: "an optional enum field still discriminates by its symbol",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltOptionalTaggedRecord(cltFast, "-f"), cltOptionalTaggedRecord(cltSolo, "-s"),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltSym: cltSolo}},
		want:   []string{"-s", cltSolo},
	},
	{
		name: "a value no record member accepts falls back to the first with its shape",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltEither, cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltUnionRecord(cltAlpha, "-a"), cltUnionRecord(cltBeta, "-b"),
				}), nil),
			},
		},
		inputs: map[string]any{cltEither: map[string]any{cltRaw: "V"}},
		want:   make([]string, 0),
	},
	{
		name: "a named type reference ends the descent",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("named", cwlcore.NewNamedType("myRecord"), cltPrefixed(0, "-n")),
			},
		},
		inputs: map[string]any{"named": map[string]any{"f": "V"}},
		want:   []string{"-n"},
	},
}

func TestBuildCommandLineSchemaLevelBindings(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltSchemaLevelBindingsCases)
}

// TestBuildCommandLineTypeValueMismatch pins the rule that the *value's* type decides the binding,
// so a schema that disagrees with what actually turned up never produces a wrong argument.
// cltTypeValueMismatchCases is the table TestBuildCommandLineTypeValueMismatch runs.
var cltTypeValueMismatchCases = []cltCase{
	{
		name: "an array schema holding a scalar binds the scalar",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltItemBound(cltPrefixed(0, "-i")), cltPrefixed(0, "-x")),
			},
		},
		inputs: map[string]any{"xs": cltSolo},
		want:   []string{"-x", cltSolo},
	},
	{
		name: "a record schema holding a scalar binds the scalar",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltRec, cwlcore.NewRecordType(&cwlcore.RecordSchema{
					Fields: []cwlcore.RecordField{{
						Name: cltToolID + cltRec + "/f", Type: cltString, InputBinding: cltPrefixed(0, "-f"),
					}},
				}), cltPrefixed(0, "-r")),
			},
		},
		inputs: map[string]any{cltRec: cltSolo},
		want:   []string{"-r", cltSolo},
	},
	{
		name: "a scalar schema holding an object adds the prefix only",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("x", cltString, cltPrefixed(0, "-x")),
			},
		},
		inputs: map[string]any{"x": map[string]any{"a": "1"}},
		want:   []string{"-x"},
	},
	{
		name: "an array schema with no schema at all binds nothing below the parameter",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cwlcore.NewArrayType(nil), cltPrefixed(0, "-x")),
			},
		},
		inputs: map[string]any{"xs": []any{"a"}},
		want:   []string{"-x"},
	},
	{
		name: "a record type with no schema at all binds nothing below the parameter",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltRec, cwlcore.NewRecordType(nil), cltPrefixed(0, "-r")),
			},
		},
		inputs: map[string]any{cltRec: map[string]any{"f": "V"}},
		want:   []string{"-r"},
	},
	{
		name: "an enum type with no schema at all binds nothing extra",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("e", cwlcore.NewEnumType(nil), cltPrefixed(0, "-e")),
			},
		},
		inputs: map[string]any{"e": cltSym},
		want:   []string{"-e", cltSym},
	},
}

func TestBuildCommandLineTypeValueMismatch(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltTypeValueMismatchCases)
}

// cltBadPosition is a binding whose position expression evaluates to something that is not an
// integer, which every level of the walk has to reject the same way.
var cltBadPosition = &cwlcore.CommandLineBinding{
	Position: cwlcore.NewExprLongExpression("${return 'nope';}"),
}

// cltNestedErrorsCases is the table TestBuildCommandLineNestedErrors runs.
var cltNestedErrorsCases = []cltErrCase{
	{
		name: "a record field's position expression",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltRec, cwlcore.NewRecordType(&cwlcore.RecordSchema{
					Fields: []cwlcore.RecordField{{
						Name: cltToolID + cltRec + "/f", Type: cltString, InputBinding: cltBadPosition,
					}},
				}), nil),
			},
		},
		inputs:  map[string]any{cltRec: map[string]any{"f": "V"}},
		wantErr: ErrBindingPosition,
	},
	{
		name: "a record schema's own position expression",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput(cltRec, cwlcore.NewRecordType(&cwlcore.RecordSchema{
					InputBinding: cltBadPosition,
				}), nil),
			},
		},
		inputs:  map[string]any{cltRec: make(map[string]any)},
		wantErr: ErrBindingPosition,
	},
	{
		name: "an enum schema's own position expression",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("e", cwlcore.NewEnumType(&cwlcore.EnumSchema{InputBinding: cltBadPosition}), nil),
			},
		},
		inputs:  map[string]any{"e": cltSym},
		wantErr: ErrBindingPosition,
	},
	{
		name: "an array element's position expression",
		tool: &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("xs", cltItemBound(cltBadPosition), nil),
			},
		},
		inputs:  map[string]any{"xs": []any{"a"}},
		wantErr: ErrBindingPosition,
	},
}

func TestBuildCommandLineNestedErrors(t *testing.T) {
	t.Parallel()

	runCltErrCases(t, cltNestedErrorsCases)
}

// TestBuildCommandLineErrorNamesItsOrigin proves a failure says which parameter it came from, at
// bind time and at render time alike.
func TestBuildCommandLineErrorNamesItsOrigin(t *testing.T) {
	t.Parallel()

	tool := &cwlcore.CommandLineTool{
		Inputs: []cwlcore.CommandInputParameter{
			cltInput(cltRec, cwlcore.NewRecordType(&cwlcore.RecordSchema{
				Fields: []cwlcore.RecordField{{
					Name: cltToolID + cltRec + "/f", Type: cltFile, InputBinding: cltAt(0),
				}},
			}), nil),
		},
	}

	_, err := BuildCommandLine(tool, map[string]any{
		cltRec: map[string]any{"f": &cwlcore.File{Location: "file:///a.txt"}},
	}, cltEval(), nil, cwlcore.RuntimeContext{})
	if err == nil {
		t.Fatal("BuildCommandLine: want an error for a File with no path")
	}

	if want := "input rec.f"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name %q", err, want)
	}
}
