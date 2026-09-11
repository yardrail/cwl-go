package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Decoding requirements and hints from validated salad nodes.

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

// requirementDecoders maps requirement class names to their decoders.
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

// hint decodes one hints entry. Unknown hints are silently accepted per spec.
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

// schemaDefRequirement decodes a SchemaDefRequirement.
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

// shellCommandRequirement decodes a ShellCommandRequirement marker.
func (*decoder) shellCommandRequirement(*salad.MapNode) ProcessRequirement {
	return &ShellCommandRequirement{requirementBase: requirementBase{}}
}

// resourceRequirement decodes a ResourceRequirement.
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
