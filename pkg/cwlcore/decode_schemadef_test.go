package cwlcore

import (
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// The declaration names the schemadef fixture and the type-name tests reach for
// often enough that spelling them out each time would be noise.
const (
	coordinateType  = "coordinate"
	shortTypeName   = "rec"
	qualifiedInFile = "file:///tool.cwl#" + shortTypeName
)

// schemaDefScope builds the requirement scope of the schemadef fixture, which
// declares three types, one of them referring to another.
func schemaDefScope(t *testing.T) *RequirementScope {
	t.Helper()

	return NewScope(decodeFixture(t, "schemadef.cwl"))
}

// scopeOf decodes an inline tool and returns its requirement scope.
func scopeOf(t *testing.T, src string) *RequirementScope {
	t.Helper()

	return NewScope(mustCommandLineTool(t, src))
}

func TestResolveSchemaDefFindsADeclaredRecord(t *testing.T) {
	t.Parallel()

	resolved, ok := ResolveSchemaDef(schemaDefScope(t), coordinateType)
	if !ok {
		t.Fatal("ResolveSchemaDef did not find the declared type")
	}

	schema := resolved.Record()
	if schema == nil {
		t.Fatalf("resolved to %s, want a record", resolved.Kind())
	}

	assertEqual(t, "Name", schema.Name, coordinateType)
	assertEqual(t, "len(Fields)", len(schema.Fields), 2)
	assertEqual(t, "Fields[0].InputBinding.Prefix", schema.Fields[0].InputBinding.Prefix, "--chr")
}

func TestResolveSchemaDefAcceptsEverySpellingOfAName(t *testing.T) {
	t.Parallel()

	scope := schemaDefScope(t)

	// A declaration is found however the reference to it was resolved: bare,
	// as a fragment, or as a whole identifier.
	spellings := []string{
		coordinateType,
		"#" + coordinateType,
		"file:///tool.cwl#" + coordinateType,
		cwlPrefix + coordinateType,
	}

	for _, name := range spellings {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resolved, ok := ResolveSchemaDef(scope, name)
			if !ok {
				t.Fatalf("ResolveSchemaDef(%q) found nothing", name)
			}

			if resolved.Kind() != TypeKindRecord {
				t.Errorf("ResolveSchemaDef(%q) resolved to %s, want a record", name, resolved.Kind())
			}
		})
	}
}

func TestResolveSchemaDefFollowsAReferenceToAnotherDeclaration(t *testing.T) {
	t.Parallel()

	resolved, ok := ResolveSchemaDef(schemaDefScope(t), "region")
	if !ok {
		t.Fatal("ResolveSchemaDef did not find the declared type")
	}

	schema := resolved.Record()
	if schema == nil {
		t.Fatalf("resolved to %s, want a record", resolved.Kind())
	}

	// Both fields name the coordinate record, one bare and one as a fragment,
	// and both must come back expanded — otherwise the nested inputBindings
	// they carry are unreachable, which is the whole point of resolving.
	for i, field := range []string{"from", "to"} {
		nested := schema.Fields[i].Type.Record()
		if nested == nil {
			t.Fatalf("field %q resolved to %s, want the coordinate record", field, schema.Fields[i].Type.Kind())
		}

		assertEqual(t, "nested Name", nested.Name, coordinateType)
		assertEqual(t, "nested Fields[0].InputBinding.Prefix", nested.Fields[0].InputBinding.Prefix, "--chr")
		assertEqual(t, "nested Fields[1].InputBinding.Prefix", nested.Fields[1].InputBinding.Prefix, "--pos")
	}

	// The field's own binding survives the substitution.
	assertEqual(t, "Fields[0].InputBinding.Position", schema.Fields[0].InputBinding.Position.Int(), int64(1))
}

func TestResolveSchemaDefReportsWhatItCannotResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		scope *RequirementScope
		name  string
		ref   string
	}{
		{name: "nil scope", scope: nil, ref: coordinateType},
		{name: "empty scope", scope: &RequirementScope{}, ref: coordinateType},
		{
			name:  "scope without a SchemaDefRequirement",
			scope: scopeOf(t, "class: CommandLineTool\ncwlVersion: v1.2\ninputs: []\noutputs: []\n"),
			ref:   coordinateType,
		},
		{
			name:  "SchemaDefRequirement declaring no types",
			scope: scopeOf(t, toolWithRequirement+"  - class: SchemaDefRequirement\n    types: []\n"),
			ref:   coordinateType,
		},
		{name: "name no declaration matches", scope: schemaDefScope(t), ref: "no_such_type"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolved, ok := ResolveSchemaDef(tc.scope, tc.ref)
			if ok {
				t.Errorf("ResolveSchemaDef reported success, resolving to %s", resolved.Kind())
			}

			if resolved.IsSet() {
				t.Errorf("ResolveSchemaDef returned %s alongside a false result", resolved.Kind())
			}
		})
	}
}

func TestResolveSchemaDefTerminatesOnASelfReference(t *testing.T) {
	t.Parallel()

	const src = toolWithRequirement + `  - class: SchemaDefRequirement
    types:
      - name: tree
        type: record
        fields:
          - name: child
            type: ["null", tree]
`

	resolved, ok := ResolveSchemaDef(scopeOf(t, src), "tree")
	if !ok {
		t.Fatal("ResolveSchemaDef did not find the declared type")
	}

	schema := resolved.Record()
	if schema == nil {
		t.Fatalf("resolved to %s, want a record", resolved.Kind())
	}

	// The edge that closes the cycle is left as the reference it was written
	// as: a recursive type has no finite expansion, and stopping here is what
	// tells the caller where the cycle is.
	options := schema.Fields[0].Type.Options()
	assertEqual(t, "len(Options())", len(options), 2)
	assertEqual(t, "Options()[1].Kind()", options[1].Kind(), TypeKindNamed)
	assertEqual(t, "Options()[1].Name()", options[1].Name(), "tree")
}

func TestResolveSchemaDefTerminatesOnAMutualCycle(t *testing.T) {
	t.Parallel()

	const src = toolWithRequirement + `  - class: SchemaDefRequirement
    types:
      - name: parent
        type: record
        fields:
          - name: kid
            type: child
      - name: child
        type: record
        fields:
          - name: owner
            type: parent
`

	resolved, ok := ResolveSchemaDef(scopeOf(t, src), "parent")
	if !ok {
		t.Fatal("ResolveSchemaDef did not find the declared type")
	}

	kid := resolved.Record().Fields[0].Type.Record()
	if kid == nil {
		t.Fatal("the parent's field did not expand into the child record")
	}

	assertEqual(t, "child Name", kid.Name, "child")
	assertEqual(t, "child owner Kind()", kid.Fields[0].Type.Kind(), TypeKindNamed)
	assertEqual(t, "child owner Name()", kid.Fields[0].Type.Name(), "parent")
}

func TestResolveSchemaDefExpandsAResolvedNameOnlyOncePerBranch(t *testing.T) {
	t.Parallel()

	// The same declaration named twice on sibling branches is expanded on
	// both: only a name on the path from the root is a cycle.
	const src = toolWithRequirement + `  - class: SchemaDefRequirement
    types:
      - name: leaf
        type: record
        fields:
          - name: value
            type: string
      - name: pair
        type: record
        fields:
          - name: left
            type: leaf
          - name: right
            type: leaf
`

	resolved, ok := ResolveSchemaDef(scopeOf(t, src), "pair")
	if !ok {
		t.Fatal("ResolveSchemaDef did not find the declared type")
	}

	for i, side := range []string{"left", "right"} {
		if resolved.Record().Fields[i].Type.Kind() != TypeKindRecord {
			t.Errorf("field %q resolved to %s, want a record", side, resolved.Record().Fields[i].Type.Kind())
		}
	}
}

func TestResolveSchemaDefLeavesAnUndeclaredNestedNameAlone(t *testing.T) {
	t.Parallel()

	// A declaration may refer to a type declared somewhere this scope does not
	// reach. Substitution leaves it as the reference it was written as rather
	// than failing, because whether that is a problem is the caller's question.
	const src = toolWithRequirement + `  - class: SchemaDefRequirement
    types:
      - name: holder
        type: record
        fields:
          - name: thing
            type: undeclared_type
`

	resolved, ok := ResolveSchemaDef(scopeOf(t, src), "holder")
	if !ok {
		t.Fatal("ResolveSchemaDef did not find the declared type")
	}

	field := resolved.Record().Fields[0].Type
	assertEqual(t, "field Kind()", field.Kind(), TypeKindNamed)
	assertEqual(t, "field Name()", field.Name(), "undeclared_type")
}

// TestSchemaDefTypesIgnoresARequirementOfTheWrongType covers schemaDefTypes'
// type-assertion guard: GetRequirement can report found=true for a
// SchemaDefRequirement class without the declaration actually being a
// *SchemaDefRequirement, if something else claimed the class first — a
// RawRequirement spoofing it, say — and that must not panic.
func TestSchemaDefTypesIgnoresARequirementOfTheWrongType(t *testing.T) {
	t.Parallel()

	scope := NewScope(tool(reqs(&RawRequirement{ClassIRI: ClassSchemaDefRequirement}), nil))

	resolved, ok := ResolveSchemaDef(scope, coordinateType)
	if ok {
		t.Errorf("ResolveSchemaDef found %s through a spoofed requirement, want false", resolved.Kind())
	}
}

// TestSchemaDefLookupSkipsNonMappingEntries covers schemaDefResolver.lookup's
// AsMap guard: a Types entry that is not a mapping is skipped rather than
// stopping the search for a real declaration alongside it.
func TestSchemaDefLookupSkipsNonMappingEntries(t *testing.T) {
	t.Parallel()

	declared := schemaDefTypes(schemaDefScope(t))
	if len(declared) == 0 {
		t.Fatal("the schemadef fixture declares no types")
	}

	spoofed := NewScope(tool(reqs(&SchemaDefRequirement{
		Types: []salad.Node{salad.NewStringNode(salad.SourceLine{}, "oops"), declared[0]},
	}), nil))

	resolved, ok := ResolveSchemaDef(spoofed, coordinateType)
	if !ok {
		t.Fatal("ResolveSchemaDef did not find the declaration past the bogus entry")
	}

	if resolved.Kind() != TypeKindRecord {
		t.Errorf("resolved to %s, want a record", resolved.Kind())
	}
}

// TestResolveTypeRefLeavesANilArrayOrRecordUnchanged covers
// schemaDefResolver.substituteArray and substituteRecord's nil-schema guards.
// NewArrayType(nil) and NewRecordType(nil) produce a TypeRef whose Array() or
// Record() accessor returns a typed nil, which substitution must return
// unchanged rather than dereferencing.
func TestResolveTypeRefLeavesANilArrayOrRecordUnchanged(t *testing.T) {
	t.Parallel()

	scope := schemaDefScope(t)

	array := NewArrayType(nil)
	if got := ResolveTypeRef(scope, array); got.Kind() != TypeKindArray || got.Array() != nil {
		t.Errorf("ResolveTypeRef(nil array) = %v, want an unchanged nil-schema array", got)
	}

	record := NewRecordType(nil)
	if got := ResolveTypeRef(scope, record); got.Kind() != TypeKindRecord || got.Record() != nil {
		t.Errorf("ResolveTypeRef(nil record) = %v, want an unchanged nil-schema record", got)
	}
}

func TestResolveTypeRefOnParameterTypes(t *testing.T) {
	t.Parallel()

	tool, ok := decodeFixture(t, "schemadef.cwl").(*CommandLineTool)
	if !ok {
		t.Fatal("decoded process is not a *CommandLineTool")
	}

	scope := NewScope(tool)

	// A bare reference expands into the record it names.
	target := ResolveTypeRef(scope, tool.Inputs[0].Type)
	assertEqual(t, "target Kind()", target.Kind(), TypeKindRecord)
	assertEqual(t, "target Name", target.Record().Name, "region")

	// So does a reference buried inside an array's item type.
	many := ResolveTypeRef(scope, tool.Inputs[1].Type)
	assertEqual(t, "many Kind()", many.Kind(), TypeKindArray)
	assertEqual(t, "many item Kind()", many.Array().Items.Kind(), TypeKindRecord)
	assertEqual(t, "many item binding", many.Array().Items.Record().Fields[0].InputBinding.Prefix, "--chr")

	// And one inside a union, which is what an optional parameter is.
	maybe := ResolveTypeRef(scope, tool.Inputs[2].Type)
	assertEqual(t, "maybe Kind()", maybe.Kind(), TypeKindUnion)
	assertEqual(t, "maybe IsOptional()", maybe.IsOptional(), true)
	assertEqual(t, "maybe Options()[1].Kind()", maybe.Options()[1].Kind(), TypeKindEnum)
	assertEqual(t, "maybe symbols", strings.Join(maybe.Options()[1].Enum().Symbols, ","), "plus,minus")

	// A type that names nothing declared is returned untouched, so a caller
	// can run this over every parameter without checking first.
	plain := ResolveTypeRef(scope, tool.Inputs[3].Type)
	assertEqual(t, "plain Kind()", plain.Kind(), TypeKindPrimitive)
	assertEqual(t, "plain Name()", plain.Name(), PrimitiveString)
}

func TestResolveTypeRefWithoutASchemaDefRequirement(t *testing.T) {
	t.Parallel()

	scope := scopeOf(t, "class: CommandLineTool\ncwlVersion: v1.2\ninputs: []\noutputs: []\n")

	named := NewNamedType(coordinateType)
	if got := ResolveTypeRef(scope, named); got.Kind() != TypeKindNamed || got.Name() != named.Name() {
		t.Errorf("ResolveTypeRef changed %s to %s with no declarations in scope", named, got)
	}
}

func TestResolveTypeRefLeavesTheDecodedTypeAlone(t *testing.T) {
	t.Parallel()

	tool, ok := decodeFixture(t, "schemadef.cwl").(*CommandLineTool)
	if !ok {
		t.Fatal("decoded process is not a *CommandLineTool")
	}

	scope := NewScope(tool)
	before := tool.Inputs[1].Type.Array().Items.Kind()

	ResolveTypeRef(scope, tool.Inputs[1].Type)

	// Substitution copies rather than edits, so the process keeps the type it
	// was decoded with and a second call resolves the same thing again.
	assertEqual(t, "Inputs[1] item Kind() after resolving", tool.Inputs[1].Type.Array().Items.Kind(), before)
	assertEqual(t, "Inputs[1] item Kind() as decoded", before, TypeKindNamed)
}

func TestTypeNameKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: shortTypeName, want: shortTypeName},
		{name: "#" + shortTypeName, want: shortTypeName},
		{name: qualifiedInFile, want: shortTypeName},
		{name: "file:///tool.cwl#main/step/" + shortTypeName, want: shortTypeName},
		{name: cwlPrefix + shortTypeName, want: shortTypeName},
		{name: saladNamespace + shortTypeName, want: shortTypeName},
		{name: "http://example.org/types/" + shortTypeName, want: shortTypeName},
		{name: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := typeNameKey(tc.name); got != tc.want {
				t.Errorf("typeNameKey(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
