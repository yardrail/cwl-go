package cwlcli

import (
	"fmt"
	"runtime/debug"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// devVersion is what a build that was not stamped reports.
const devVersion = "(devel)"

// VersionText returns the version banner for tool.
func VersionText(tool string) string {
	return fmt.Sprintf("%s %s\nCWL %s (schema snapshot %s)",
		tool, buildVersion(), cwlcore.CWLVersionV12, cwlcore.SchemaVersion())
}

// buildVersion reports the module version, or devVersion for untagged builds.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return devVersion
	}

	return info.Main.Version
}
