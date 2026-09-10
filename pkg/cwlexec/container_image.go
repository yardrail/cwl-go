package cwlexec

import "github.com/yardrail/cwl-go/pkg/cwlcore"

// imageReference is the name the tool is run from, and the tag anything built, loaded or imported
// is given.
//
// cwltool's get_image opens by filling a missing dockerImageId in from dockerPull (docker.py:113),
// which is what makes one name serve both purposes: `dockerPull: alpine` runs `alpine`, and
// `dockerFile` with `dockerImageId: mine` builds and then runs `mine`. dockerImageId on its own
// means "run what is already here", and is the only field that names nothing to fetch.
func imageReference(declared *cwlcore.DockerRequirement) string {
	if declared.DockerImageID != "" {
		return declared.DockerImageID
	}

	return declared.DockerPull
}
