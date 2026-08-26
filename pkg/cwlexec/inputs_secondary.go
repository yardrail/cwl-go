package cwlexec

import (
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The step half of the secondaryFiles rule, whose top-level half lives in joborder_secondary.go.
//
// That file sets the distinction out in full; the short form is that a declared pattern is
// *discovered* at the top level and *required* inside a workflow step. A step's input object is
// assembled by the scheduler from what upstream steps produced, never by [ParseJobOrder], so the
// discovery pass never sees it — and it must not, because a companion that happens to be sitting on
// disk beside the primary is not one the enclosing document supplied.
//
// So this pass goes nowhere near the filesystem. Every judgement it makes is about what is already
// attached to the File object the step was handed. What it does share with the discovery pass is
// every *rule*: which name a pattern derives ([joSecondaryPass.candidates], and through it
// outTrimOptionalMarker and outSubstitutePattern), whether a missing companion matters
// ([joSecondaryPass.policy], including the expression-valued and null cases), and what counts as
// already attached ([joAlreadyPresent]). Those are reused rather than restated: the two sides
// disagreeing about what ".s2" means, or about what an unspecified `required` defaults to, is
// exactly the drift this arrangement exists to prevent.

// stepSecondaryDecl is one input parameter of the process under run:, reduced to what the step-side
// check needs: the name that keys the input object, the fully resolved declared type — resolved
// once, at planning time, so that a SchemaDefRequirement record's field-level patterns are reachable
// — and the parameter's own patterns.
type stepSecondaryDecl struct {
	// name is the input short name.
	name string

	// schemas are the parameter's own secondaryFiles patterns, empty when it declares none.
	schemas []cwlcore.SecondaryFileSchema

	// typ is the declared type with every named reference resolved, which is what carries an
	// array's item declaration and a record's field declarations.
	typ cwlcore.TypeRef
}

// stepSecondaryDecls collects the declarations the step-side check runs against.
//
// Resolution happens here, at planning time, rather than per invocation: a thousand-way scatter
// invokes the same step a thousand times against declarations that cannot have changed.
func stepSecondaryDecls(run cwlcore.Process, scope *cwlcore.RequirementScope) []stepSecondaryDecl {
	inputs := joDeclaredInputs(run)
	decls := make([]stepSecondaryDecl, 0, len(inputs))

	for index := range inputs {
		in := &inputs[index]
		decls = append(decls, stepSecondaryDecl{
			name:    in.name,
			schemas: in.secondary,
			typ:     cwlcore.ResolveTypeRef(scope, in.typ),
		})
	}

	return decls
}

// checkStepSecondaryFiles reports a File in a step's input object that is bound to a parameter
// declaring a required secondary file the File does not carry.
//
// eval evaluates an expression-valued pattern or `required`, and object is bound to `inputs` while
// it does, exactly as it is during discovery.
func checkStepSecondaryFiles(
	decls []stepSecondaryDecl, object map[string]any, eval *cwlcore.Evaluator,
) error {
	if len(decls) == 0 {
		return nil
	}

	check := &stepSecondaryPass{
		rules: &joSecondaryPass{eval: eval, inputs: outExpressionObject(object)},
	}

	for index := range decls {
		decl := &decls[index]

		err := check.value(object[decl.name], decl.typ, decl.schemas, decl.name)
		if err != nil {
			return err
		}
	}

	return nil
}

// stepSecondaryPass is one check pass, which is the discovery pass's rules with a different terminal
// step: rules answers what a pattern names and whether it matters, and this decides what to do about
// a name that is not there.
type stepSecondaryPass struct {
	rules *joSecondaryPass
}

// value applies the patterns governing one position to the value at it, descending an array and a
// record on exactly the terms [joSecondaryPass.value] does — value-driven, with the declared type
// carried alongside only for what the value cannot say.
func (s *stepSecondaryPass) value(
	value any, typ cwlcore.TypeRef, schemas []cwlcore.SecondaryFileSchema, path string,
) error {
	switch typed := value.(type) {
	case *cwlcore.File:
		return s.file(typed, schemas, path)
	case []any:
		return s.items(typed, typ, schemas, path)
	case map[string]any:
		return s.record(typed, typ, path)
	default:
		return nil
	}
}

// items applies an array declaration's patterns element by element.
//
// A scattered parameter arrives here still holding its whole array, because the check runs while the
// step's input object is being built and before it is expanded into sub-jobs. That costs nothing and
// reads the same either way: [joArrayItems] leaves a non-array declaration alone, so each element is
// checked against the item patterns whether the declaration was `File[]` scattered over or `File`
// about to be.
func (s *stepSecondaryPass) items(
	values []any, typ cwlcore.TypeRef, schemas []cwlcore.SecondaryFileSchema, path string,
) error {
	items := joArrayItems(typ)

	for index, value := range values {
		err := s.value(value, items, schemas, fmt.Sprintf("%s[%d]", path, index))
		if err != nil {
			return err
		}
	}

	return nil
}

// record descends a record value field by field, since a record carries no patterns of its own and
// its Files are its fields' values.
func (s *stepSecondaryPass) record(values map[string]any, typ cwlcore.TypeRef, path string) error {
	schema := joRecordFields(typ)
	if schema == nil {
		return nil
	}

	for index := range schema.Fields {
		field := &schema.Fields[index]
		name := ShortName(field.Name)

		err := s.value(values[name], field.Type, field.SecondaryFiles, path+"."+name)
		if err != nil {
			return err
		}
	}

	return nil
}

// file checks every pattern governing a position against one primary File.
//
// A File with no path is skipped, on the same terms the discovery pass skips one: it is a file
// literal or a resource on another host, which has not been staged yet and so has no directory any
// companion could have been supplied from. Blaming the pattern for that would fail a document whose
// companion is about to be created.
func (s *stepSecondaryPass) file(primary *cwlcore.File, schemas []cwlcore.SecondaryFileSchema, path string) error {
	if primary == nil || len(schemas) == 0 || primary.Path == "" {
		return nil
	}

	for index := range schemas {
		err := s.pattern(primary, &schemas[index], path)
		if err != nil {
			return err
		}
	}

	return nil
}

// pattern checks one declared pattern against one primary File.
func (s *stepSecondaryPass) pattern(
	primary *cwlcore.File, schema *cwlcore.SecondaryFileSchema, path string,
) error {
	self := outFileObject(primary)

	policy, policyErr := s.rules.policy(schema, self, path)
	if policyErr != nil {
		return policyErr
	}

	// Only a required pattern has anything to say here. An optional one describes a companion the
	// document was content to do without, and there is no disk read to attach it from.
	if policy != outSecondaryRequired {
		return nil
	}

	candidates, candidateErr := s.rules.candidates(schema, primary, self, path)
	if candidateErr != nil {
		return candidateErr
	}

	for _, candidate := range candidates {
		err := s.candidate(candidate, primary, path)
		if err != nil {
			return err
		}
	}

	return nil
}

// candidate reports one required companion that is not attached to the primary File.
//
// Two candidates are let through. A null names no file at all — Process.yml: "The expression may
// return 'null' in which case there is no secondaryFile from that expression" — so a required
// pattern is not violated by one. An object *is* the companion rather than a name for one: the
// expression that produced it supplied the value, which is the reference implementation's reading
// too, and the file it names may sit anywhere, including somewhere only staging will reach.
func (s *stepSecondaryPass) candidate(candidate any, primary *cwlcore.File, path string) error {
	if candidate == nil {
		return nil
	}

	if _, supplied := candidate.(map[string]any); supplied {
		return nil
	}

	ref, refErr := joCandidateRef(candidate, primary, path)
	if refErr != nil {
		return refErr
	}

	if joAlreadyPresent(primary.SecondaryFiles, ref.name) {
		return nil
	}

	return fmt.Errorf("%s: %w: %s is not attached to %s", path, ErrSecondaryMissing, ref.name, primary.Basename)
}
