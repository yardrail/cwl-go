package cwlcore

import "slices"

// The inheritance-validity filter, and the class tables it and CheckKnown share.
//
// The specification does not let every requirement fall through a workflow onto
// whatever runs beneath it:
//
//	Requirements specified in a parent Workflow are inherited by step processes
//	if they are valid for that step. If the substep is a CommandLineTool only
//	the InlineJavascriptRequirement, SchemaDefRequirement, DockerRequirement,
//	SoftwareRequirement, InitialWorkDirRequirement, EnvVarRequirement,
//	ShellCommandRequirement, ResourceRequirement, LoadListingRequirement,
//	WorkReuse, NetworkAccess, InplaceUpdateRequirement, ToolTimeLimit are valid.
//
// Those thirteen are exactly the seventeen core classes minus the four workflow
// features — SubworkflowFeatureRequirement, ScatterFeatureRequirement,
// MultipleInputFeatureRequirement and StepInputExpressionRequirement — which is
// what one would expect: they describe things only a workflow or a workflow
// step can do, so a tool has no use for them. A workflow that enables scatter
// for its steps must not have that leak into every tool underneath it.
//
// CommandLineTool is the only class the specification restricts, so it is the
// only class restricted here. An ExpressionTool or an Operation step inherits
// everything, and so does a process of an extension class, because inventing a
// restriction the specification does not state would silently drop declarations
// a conforming document was entitled to make.

// commandLineToolRequirements is the specification's list of core requirement
// classes a CommandLineTool step inherits from an enclosing workflow.
var commandLineToolRequirements = map[string]bool{
	ClassInlineJavascriptRequirement: true,
	ClassSchemaDefRequirement:        true,
	ClassDockerRequirement:           true,
	ClassSoftwareRequirement:         true,
	ClassInitialWorkDirRequirement:   true,
	ClassEnvVarRequirement:           true,
	ClassShellCommandRequirement:     true,
	ClassResourceRequirement:         true,
	ClassLoadListingRequirement:      true,
	ClassWorkReuse:                   true,
	ClassNetworkAccess:               true,
	ClassInplaceUpdateRequirement:    true,
	ClassToolTimeLimit:               true,
}

// coreRequirements is every requirement class CWL v1.2 defines itself. Anything
// outside it is an extension, and reaches this package as a [RawRequirement] or
// a [RawHint].
var coreRequirements = map[string]bool{
	ClassInlineJavascriptRequirement:     true,
	ClassSchemaDefRequirement:            true,
	ClassLoadListingRequirement:          true,
	ClassDockerRequirement:               true,
	ClassSoftwareRequirement:             true,
	ClassInitialWorkDirRequirement:       true,
	ClassEnvVarRequirement:               true,
	ClassShellCommandRequirement:         true,
	ClassResourceRequirement:             true,
	ClassWorkReuse:                       true,
	ClassNetworkAccess:                   true,
	ClassInplaceUpdateRequirement:        true,
	ClassToolTimeLimit:                   true,
	ClassSubworkflowFeatureRequirement:   true,
	ClassScatterFeatureRequirement:       true,
	ClassMultipleInputFeatureRequirement: true,
	ClassStepInputExpressionRequirement:  true,
}

// IsCoreRequirement reports whether class names one of the seventeen
// requirement classes CWL v1.2 defines. It is false for every extension class,
// including one a downstream package fully understands — vouching for those is
// what the allowExtensions argument of [RequirementScope.CheckKnown] is for.
func IsCoreRequirement(class string) bool {
	return coreRequirements[class]
}

// validForCommandLineTool reports whether a requirement or hint of the given
// class may be inherited by a CommandLineTool.
//
// An extension class always may. The specification's list enumerates the core
// vocabulary it defines and says nothing about classes added by an extension,
// and reading it as a closed world would make an inherited extension
// requirement unreachable from any tool — which would defeat the extension
// mechanism this package is built around. Whether an extension requirement is
// understood at all is a separate question, and CheckKnown's.
func validForCommandLineTool(class string) bool {
	return commandLineToolRequirements[class] || !IsCoreRequirement(class)
}

// filterFrames applies the inheritance-validity filter to a frame chain,
// returning the frames a lookup should see.
//
// The filter runs here, on push, and therefore strictly before the class
// deduplication that [RequirementScope.EffectiveRequirements] performs. That
// order is the point: deduplication picks a per-class winner, and it must pick
// among declarations that are allowed to apply. Filtering afterwards would have
// to remember which frame each winner came from in order to know whether it was
// inherited or declared on the target itself — bookkeeping that filtering first
// makes unnecessary.
//
// Only entries strictly outside the target frame are filtered. Entries the
// target process declared on itself, and any pushed inside it, are not
// inherited from anywhere, so the rule does not reach them.
func filterFrames(frames []reqFrame) []reqFrame {
	target := targetIndex(frames)
	if target < 0 || frames[target].class != ClassCommandLineTool {
		return frames
	}

	out := make([]reqFrame, len(frames))
	copy(out, frames)

	for i := range target {
		out[i].reqs = keepValid(out[i].reqs)
		out[i].hints = keepValid(out[i].hints)
	}

	return out
}

// targetIndex returns the index of the inner-most frame that knows its process
// class — the process the scope resolves for — or -1 if no frame does.
func targetIndex(frames []reqFrame) int {
	for i, f := range slices.Backward(frames) {
		if f.class != "" {
			return i
		}
	}

	return -1
}

// keepValid drops the entries a CommandLineTool does not inherit.
func keepValid[T Hint](entries []T) []T {
	out := make([]T, 0, len(entries))

	for _, e := range entries {
		if validForCommandLineTool(e.Class()) {
			out = append(out, e)
		}
	}

	return out
}
