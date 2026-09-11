package cwlexec

import "github.com/yardrail/cwl-go/pkg/cwlcore"

// imageReference returns dockerImageId if set, otherwise dockerPull.
func imageReference(declared *cwlcore.DockerRequirement) string {
	if declared.DockerImageID != "" {
		return declared.DockerImageID
	}

	return declared.DockerPull
}
