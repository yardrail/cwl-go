package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Requirements and hints.
//
// A requirements entry whose class this package models decodes into that class's
// struct; anything else becomes a RawRequirement. Neither is an error here.
// Whether a document is schema-valid was pkg/salad's question and has already
// been answered, and whether an unrecognized requirement is fatal is a question
// about the runner's capabilities rather than about the document — requirements.go
// makes that call, after a downstream package has had its chance to claim the
// class.
//
// Hints are more permissive still: WorkflowStep.hints is typed Any[] by the
// schema, so an entry naming an unknown class, or holding something that is not
// even a mapping, is expected rather than exceptional and never records an error.

// Keys the requirement records add to the shared field set.
const (
	keyExpressionLib           = "expressionLib"
	keyTypes                   = "types"
	keyDockerPull              = "dockerPull"
	keyDockerLoad              = "dockerLoad"
	keyDockerFile              = "dockerFile"
	keyDockerImport            = "dockerImport"
	keyDockerImageID           = "dockerImageId"
	keyDockerOutputDirectory   = "dockerOutputDirectory"
	keyPackages                = "packages"
	keyPackage                 = "package"
	keyVersion                 = "version"
	keySpecs                   = "specs"
	keyEnvDef                  = "envDef"
	keyEnvName                 = "envName"
	keyEnvValue                = "envValue"
	keyCoresMin                = "coresMin"
	keyCoresMax                = "coresMax"
	keyRAMMin                  = "ramMin"
	keyRAMMax                  = "ramMax"
	keyTmpdirMin               = "tmpdirMin"
	keyTmpdirMax               = "tmpdirMax"
	keyOutdirMin               = "outdirMin"
	keyOutdirMax               = "outdirMax"
	keyEnableReuse             = "enableReuse"
	keyNetworkAccessField      = "networkAccess"
	keyInplaceUpdateField      = "inplaceUpdate"
	keyTimelimit               = "timelimit"
	whatRequirementDeclaration = "a requirement"
)

// requirementDecoders maps a requirement class to the decoder for it.
//
// It is a table rather than a switch because seventeen classes is well past what
// revive's cyclomatic-complexity limit allows in one function, and because the
// same table serves both requirements and hints: a hints entry may name any core
// requirement class, and when it does it decodes into the same struct.
var requirementDecoders = map[string]func(*decoder, *salad.MapNode) ProcessRequirement{
	ClassInlineJavascriptRequirement:     (*decoder).inlineJavascriptRequirement,
	ClassSchemaDefRequirement:            (*decoder).schemaDefRequirement,
	ClassLoadListingRequirement:          (*decoder).loadListingRequirement,
	ClassDockerRequirement:               (*decoder).dockerRequirement,
	ClassSoftwareRequirement:             (*decoder).softwareRequirement,
	ClassInitialWorkDirRequirement:       (*decoder).initialWorkDirRequirement,
	ClassEnvVarRequirement:               (*decoder).envVarRequirement,
	ClassShellCommandRequirement:         (*decoder).shellCommandRequirement,
	ClassResourceRequirement:             (*decoder).resourceRequirement,
	ClassWorkReuse:                       (*decoder).workReuse,
	ClassNetworkAccess:                   (*decoder).networkAccess,
	ClassInplaceUpdateRequirement:        (*decoder).inplaceUpdateRequirement,
	ClassToolTimeLimit:                   (*decoder).toolTimeLimit,
	ClassSubworkflowFeatureRequirement:   (*decoder).subworkflowFeatureRequirement,
	ClassScatterFeatureRequirement:       (*decoder).scatterFeatureRequirement,
	ClassMultipleInputFeatureRequirement: (*decoder).multipleInputFeatureRequirement,
	ClassStepInputExpressionRequirement:  (*decoder).stepInputExpressionRequirement,
}

// requirements decodes a requirements field.
func (d *decoder) requirements(m *salad.MapNode, key string) []ProcessRequirement {
	return decodeEach(d.listItems(m, key, keyClass, ""), d.requirement)
}

// requirement decodes one requirements entry.
func (d *decoder) requirement(node salad.Node) ProcessRequirement {
	m := d.mapping(node, whatRequirementDeclaration)
	if m == nil {
		return nil
	}

	class := d.text(m, keyClass)
	if decode, ok := requirementDecoders[shortName(class)]; ok {
		return decode(d, m)
	}

	return &RawRequirement{requirementBase: requirementBase{}, Node: node, ClassIRI: class}
}

// hints decodes a hints field.
func (d *decoder) hints(m *salad.MapNode, key string) []Hint {
	return decodeEach(d.listItems(m, key, keyClass, ""), d.hint)
}

// hint decodes one hints entry, and never records an error: the specification
// requires an implementation to ignore a hint it does not understand rather than
// reject the document.
func (d *decoder) hint(node salad.Node) Hint {
	m, ok := salad.AsMap(node)
	if !ok {
		return &RawHint{Node: node, ClassIRI: ""}
	}

	class := lenientText(m, keyClass)
	if decode, found := requirementDecoders[shortName(class)]; found {
		return decode(d, m)
	}

	return &RawHint{Node: node, ClassIRI: class}
}

// inlineJavascriptRequirement decodes an InlineJavascriptRequirement.
func (d *decoder) inlineJavascriptRequirement(m *salad.MapNode) ProcessRequirement {
	return &InlineJavascriptRequirement{
		requirementBase: requirementBase{},
		ExpressionLib:   d.textList(m, keyExpressionLib),
	}
}

// schemaDefRequirement decodes a SchemaDefRequirement. Its type schemas stay
// salad nodes: one of them may refer by name to another declared alongside it,
// and untangling that needs the whole requirement scope rather than one node.
func (d *decoder) schemaDefRequirement(m *salad.MapNode) ProcessRequirement {
	return &SchemaDefRequirement{requirementBase: requirementBase{}, Types: d.listItems(m, keyTypes, keyName, keyType)}
}

// loadListingRequirement decodes a LoadListingRequirement.
func (d *decoder) loadListingRequirement(m *salad.MapNode) ProcessRequirement {
	return &LoadListingRequirement{
		requirementBase: requirementBase{},
		LoadListing:     LoadListingEnum(d.text(m, keyLoadListing)),
	}
}

// dockerRequirement decodes a DockerRequirement.
func (d *decoder) dockerRequirement(m *salad.MapNode) ProcessRequirement {
	return &DockerRequirement{
		requirementBase:       requirementBase{},
		DockerPull:            d.text(m, keyDockerPull),
		DockerLoad:            d.text(m, keyDockerLoad),
		DockerFile:            d.text(m, keyDockerFile),
		DockerImport:          d.text(m, keyDockerImport),
		DockerImageID:         d.text(m, keyDockerImageID),
		DockerOutputDirectory: d.text(m, keyDockerOutputDirectory),
	}
}

// softwareRequirement decodes a SoftwareRequirement.
func (d *decoder) softwareRequirement(m *salad.MapNode) ProcessRequirement {
	return &SoftwareRequirement{
		requirementBase: requirementBase{},
		Packages:        decodeEach(d.listItems(m, keyPackages, keyPackage, keySpecs), d.softwarePackage),
	}
}

// softwarePackage decodes one entry of a SoftwareRequirement.
func (d *decoder) softwarePackage(node salad.Node) SoftwarePackage {
	m := d.mapping(node, "a software package")

	return SoftwarePackage{
		Package: d.text(m, keyPackage),
		Version: d.textList(m, keyVersion),
		Specs:   d.textList(m, keySpecs),
	}
}

// initialWorkDirRequirement decodes an InitialWorkDirRequirement.
func (d *decoder) initialWorkDirRequirement(m *salad.MapNode) ProcessRequirement {
	return &InitialWorkDirRequirement{requirementBase: requirementBase{}, Listing: d.initialWorkDirListing(m)}
}

// envVarRequirement decodes an EnvVarRequirement.
func (d *decoder) envVarRequirement(m *salad.MapNode) ProcessRequirement {
	return &EnvVarRequirement{
		requirementBase: requirementBase{},
		EnvDef:          decodeEach(d.listItems(m, keyEnvDef, keyEnvName, keyEnvValue), d.environmentDef),
	}
}

// environmentDef decodes one environment variable of an EnvVarRequirement.
func (d *decoder) environmentDef(node salad.Node) EnvironmentDef {
	m := d.mapping(node, "an environment variable definition")

	return EnvironmentDef{
		EnvName:  d.text(m, keyEnvName),
		EnvValue: d.expression(m, keyEnvValue),
	}
}

// shellCommandRequirement decodes a ShellCommandRequirement, which is a marker
// with no fields beyond its class.
func (*decoder) shellCommandRequirement(*salad.MapNode) ProcessRequirement {
	return &ShellCommandRequirement{requirementBase: requirementBase{}}
}

// resourceRequirement decodes a ResourceRequirement. Every field may legitimately
// be unset: the schema declines to declare defaults for the minima so that an
// implementation can tell a value that was not provided from one that happens to
// equal the documented default.
func (d *decoder) resourceRequirement(m *salad.MapNode) ProcessRequirement {
	return &ResourceRequirement{
		requirementBase: requirementBase{},
		CoresMin:        d.resourceValue(m, keyCoresMin),
		CoresMax:        d.resourceValue(m, keyCoresMax),
		RAMMin:          d.resourceValue(m, keyRAMMin),
		RAMMax:          d.resourceValue(m, keyRAMMax),
		TmpdirMin:       d.resourceValue(m, keyTmpdirMin),
		TmpdirMax:       d.resourceValue(m, keyTmpdirMax),
		OutdirMin:       d.resourceValue(m, keyOutdirMin),
		OutdirMax:       d.resourceValue(m, keyOutdirMax),
	}
}

// workReuse decodes a WorkReuse requirement.
func (d *decoder) workReuse(m *salad.MapNode) ProcessRequirement {
	return &WorkReuse{requirementBase: requirementBase{}, EnableReuse: d.exprBool(m, keyEnableReuse)}
}

// networkAccess decodes a NetworkAccess requirement.
func (d *decoder) networkAccess(m *salad.MapNode) ProcessRequirement {
	return &NetworkAccess{requirementBase: requirementBase{}, NetworkAccess: d.exprBool(m, keyNetworkAccessField)}
}

// inplaceUpdateRequirement decodes an InplaceUpdateRequirement.
func (d *decoder) inplaceUpdateRequirement(m *salad.MapNode) ProcessRequirement {
	return &InplaceUpdateRequirement{
		requirementBase: requirementBase{},
		InplaceUpdate:   d.flag(m, keyInplaceUpdateField),
	}
}

// toolTimeLimit decodes a ToolTimeLimit requirement.
func (d *decoder) toolTimeLimit(m *salad.MapNode) ProcessRequirement {
	return &ToolTimeLimit{requirementBase: requirementBase{}, Timelimit: d.exprLong(m, keyTimelimit)}
}

// subworkflowFeatureRequirement decodes a SubworkflowFeatureRequirement marker.
func (*decoder) subworkflowFeatureRequirement(*salad.MapNode) ProcessRequirement {
	return &SubworkflowFeatureRequirement{requirementBase: requirementBase{}}
}

// scatterFeatureRequirement decodes a ScatterFeatureRequirement marker.
func (*decoder) scatterFeatureRequirement(*salad.MapNode) ProcessRequirement {
	return &ScatterFeatureRequirement{requirementBase: requirementBase{}}
}

// multipleInputFeatureRequirement decodes a MultipleInputFeatureRequirement marker.
func (*decoder) multipleInputFeatureRequirement(*salad.MapNode) ProcessRequirement {
	return &MultipleInputFeatureRequirement{requirementBase: requirementBase{}}
}

// stepInputExpressionRequirement decodes a StepInputExpressionRequirement marker.
func (*decoder) stepInputExpressionRequirement(*salad.MapNode) ProcessRequirement {
	return &StepInputExpressionRequirement{requirementBase: requirementBase{}}
}
