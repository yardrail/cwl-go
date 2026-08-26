package cwlexec

import (
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// A parameter may be typed by name rather than inline, the name being one a SchemaDefRequirement
// declares. Every binding nested inside such a declaration — a record's per-field inputBinding, an
// array's per-item one — has to reach the command line exactly as an inline schema's would, which
// is what these tables pin. This is the schema_def conformance tag.

// The field names and flags the record fixture uses, spelled once each.
const (
	cltChr    = "chr"
	cltPos    = "pos"
	cltChrOpt = "--chr"
	cltChr1   = "chr1"
	cltPosOpt = "--pos"
)

// cltRecordDefs declares a record whose fields carry bindings of their own, and a second record
// referring to the first so that a declaration reached through another is expanded too.
const cltRecordDefs = `
- name: coordinate
  type: record
  fields:
    - name: pos
      type: int
      inputBinding:
        prefix: "--pos"
        position: 2
    - name: chr
      type: string
      inputBinding:
        prefix: "--chr"
        position: 1
- name: region
  type: record
  fields:
    - name: from
      type: coordinate
      inputBinding:
        prefix: "--from"
        position: 1
`

// cltArrayDef declares an array type whose *items* carry the binding, the other shape a named type
// can take.
const cltArrayDef = `
- name: names
  type: array
  items: string
  inputBinding:
    prefix: "-n"
`

// cltRecursiveDef declares a record that refers to itself, which has no finite expansion.
const cltRecursiveDef = `
- name: node
  type: record
  fields:
    - name: label
      type: string
      inputBinding:
        prefix: "-l"
    - name: child
      type: ["null", node]
      inputBinding:
        prefix: "-c"
`

// cltRecordScope is the scope declaring cltRecordDefs.
func cltRecordScope(t *testing.T) *cwlcore.RequirementScope {
	t.Helper()

	return cltSchemaDefScope(t, cltRecordDefs)
}

// cltArrayScope is the scope declaring cltArrayDef.
func cltArrayScope(t *testing.T) *cwlcore.RequirementScope {
	t.Helper()

	return cltSchemaDefScope(t, cltArrayDef)
}

// cltRecursiveScope is the scope declaring cltRecursiveDef.
func cltRecursiveScope(t *testing.T) *cwlcore.RequirementScope {
	t.Helper()

	return cltSchemaDefScope(t, cltRecursiveDef)
}

// cltSchemaDefCases is the table TestBuildCommandLineSchemaDef runs.
var cltSchemaDefCases = []cltCase{
	{
		name:  "a named record's field bindings reach the command line in position order",
		scope: cltRecordScope,
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("c", cwlcore.NewNamedType("coordinate"), cltPrefixed(0, "-c")),
			},
		},
		inputs: map[string]any{"c": map[string]any{cltChr: cltChr1, cltPos: int64(42)}},
		want:   []string{"t", "-c", cltChrOpt, cltChr1, cltPosOpt, "42"},
	},
	{
		name:  "a named record reached through another is expanded too",
		scope: cltRecordScope,
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("r", cwlcore.NewNamedType("region"), cltAt(0)),
			},
		},
		inputs: map[string]any{
			"r": map[string]any{
				"from": map[string]any{cltChr: "chrX", cltPos: int64(7)},
			},
		},
		// The field's own binding renders the nested record as its prefix alone, and the
		// coordinate record's own fields follow it, ordered by their own positions.
		want: []string{"t", "--from", cltChrOpt, "chrX", cltPosOpt, "7"},
	},
	{
		name:  "a named record reached through an optional union is expanded",
		scope: cltRecordScope,
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("c", cwlcore.NewUnionType([]cwlcore.TypeRef{
					cltNull, cwlcore.NewNamedType("coordinate"),
				}), cltAt(0)),
			},
		},
		inputs: map[string]any{"c": map[string]any{cltChr: "chr2", cltPos: int64(1)}},
		want:   []string{"t", cltChrOpt, "chr2", cltPosOpt, "1"},
	},
	{
		name:  "a named record inside an inline array is expanded per element",
		scope: cltRecordScope,
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("cs", cwlcore.NewArrayType(&cwlcore.ArraySchema{
					Items: cwlcore.NewNamedType("coordinate"),
				}), cltPrefixed(0, "-a")),
			},
		},
		inputs: map[string]any{
			"cs": []any{
				map[string]any{cltChr: "c1", cltPos: int64(1)},
				map[string]any{cltChr: "c2", cltPos: int64(2)},
			},
		},
		want: []string{"t", "-a", cltChrOpt, "c1", cltPosOpt, "1", cltChrOpt, "c2", cltPosOpt, "2"},
	},
	{
		name:  "a named array's item binding is repeated once per element",
		scope: cltArrayScope,
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("ns", cwlcore.NewNamedType("names"), cltPrefixed(0, "-x")),
			},
		},
		inputs: map[string]any{"ns": []any{"a", "b"}},
		want:   []string{"t", "-x", "-n", "a", "-n", "b"},
	},
	{
		name:  "a named array with no parameter binding still binds its items",
		scope: cltArrayScope,
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("ns", cwlcore.NewNamedType("names"), nil),
			},
		},
		inputs: map[string]any{"ns": []any{"a", "b"}},
		want:   []string{"t", "-n", "a", "-n", "b"},
	},
	{
		name:  "a name the scope does not declare still ends the descent",
		scope: cltRecordScope,
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("u", cwlcore.NewNamedType("undeclared"), cltPrefixed(0, "-u")),
			},
		},
		inputs: map[string]any{"u": map[string]any{"f": "V"}},
		want:   []string{"t", "-u"},
	},
	{
		name:  "a recursive declaration terminates at the edge that closes its cycle",
		scope: cltRecursiveScope,
		tool: &cwlcore.CommandLineTool{
			BaseCommand: []string{"t"},
			Inputs: []cwlcore.CommandInputParameter{
				cltInput("n", cwlcore.NewNamedType("node"), cltAt(0)),
			},
		},
		inputs: map[string]any{
			"n": map[string]any{
				cltLabel: "top",
				"child":  map[string]any{cltLabel: "kid"},
			},
		},
		// The outer record expands, so both its fields bind — "child" before "label",
		// their positions being equal and the tie broken by field name. The child's own
		// type is the reference that closes the cycle, so nothing below it is walked and
		// "kid" never reaches the command line. That is the only finite answer, and the
		// row exists mainly to prove the walk terminates at all.
		want: []string{"t", "-c", "-l", "top"},
	},
}

func TestBuildCommandLineSchemaDef(t *testing.T) {
	t.Parallel()

	runCltCases(t, cltSchemaDefCases)
}

// TestBuildCommandLineSchemaDefIsScoped proves the resolution really comes from the scope: the same
// tool built without one leaves the named type unresolved, so the nested bindings vanish.
func TestBuildCommandLineSchemaDefIsScoped(t *testing.T) {
	t.Parallel()

	tool := &cwlcore.CommandLineTool{
		Inputs: []cwlcore.CommandInputParameter{
			cltInput("c", cwlcore.NewNamedType("coordinate"), cltPrefixed(0, "-c")),
		},
	}
	inputs := map[string]any{"c": map[string]any{cltChr: cltChr1, cltPos: int64(42)}}

	unscoped := cltCase{tool: tool, inputs: inputs, want: []string{"-c"}}
	unscoped.run(t)

	scoped := cltCase{
		tool:   tool,
		inputs: inputs,
		scope:  cltRecordScope,
		want:   []string{"-c", cltChrOpt, cltChr1, cltPosOpt, "42"},
	}
	scoped.run(t)
}
