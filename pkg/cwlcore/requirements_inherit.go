package cwlcore

import "slices"

// Inheritance-validity filter: CommandLineTool inherits only the 13 core
// requirement classes the spec allows (everything except the 4 workflow features).
// Extension classes always inherit.

// commandLineToolRequirements lists core classes a CommandLineTool inherits.
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

// coreRequirements is every CWL v1.2 requirement class.
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

// IsCoreRequirement reports whether class is one of the 17 CWL v1.2 requirement classes.
func IsCoreRequirement(class string) bool {
	return coreRequirements[class]
}

// validForCommandLineTool reports whether a class may be inherited by a CommandLineTool.
// Extension classes always pass.
func validForCommandLineTool(class string) bool {
	return commandLineToolRequirements[class] || !IsCoreRequirement(class)
}

// filterFrames applies the inheritance-validity filter.
// Only outer frames are filtered; the target's own declarations pass through.
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

// targetIndex returns the inner-most frame with a known process class, or -1.
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
