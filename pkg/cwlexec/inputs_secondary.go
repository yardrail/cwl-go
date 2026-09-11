package cwlexec

import (
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Step-side secondaryFiles check: verifies required companions are already attached.
// Unlike the top-level pass in joborder_secondary.go, this does no filesystem I/O.

// stepSecondaryDecl is an input parameter's name, resolved type, and secondaryFiles patterns.
type stepSecondaryDecl struct {
	// name is the input short name.
	name string

	// schemas are the parameter's own secondaryFiles patterns, empty when it declares none.
	schemas []cwlcore.SecondaryFileSchema

	// typ is the fully resolved declared type.
	typ cwlcore.TypeRef
}

// stepSecondaryDecls collects input declarations for the step-side check, resolving types once.
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

// checkStepSecondaryFiles checks that required secondary files are attached to step inputs.
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

// stepSecondaryPass checks attached secondary files using discovery-pass rules.
type stepSecondaryPass struct {
	rules *joSecondaryPass
}

// value applies patterns to one value, descending into arrays and records.
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

// items applies patterns element by element over an array.
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

// record descends a record value field by field.
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

// file checks every pattern against one primary File. Skips nil files and those with no path.
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
	self := cwlcore.ToExpressionValue(primary)

	policy, policyErr := s.rules.policy(schema, self, path)
	if policyErr != nil {
		return policyErr
	}

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

// candidate checks that one required companion is attached. Null and object candidates pass through.
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
