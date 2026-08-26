package cwlcli

import (
	"fmt"
	"runtime/debug"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// devVersion is what a build that was not stamped reports.
const devVersion = "(devel)"

// VersionText returns the version banner for tool.
//
// Three facts belong in a bug report and none of them is deducible from the
// others: which build of cwl-go is running, which CWL document version it
// accepts, and which upstream schema snapshot it validates against. A tool that
// prints only the first cannot explain why a document that validates on one
// machine fails on another.
func VersionText(tool string) string {
	return fmt.Sprintf("%s %s\nCWL %s (schema snapshot %s)",
		tool, buildVersion(), cwlcore.CWLVersionV12, cwlcore.SchemaVersion())
}

// buildVersion reports the module version this binary was built from, as
// recorded by the go tool, or devVersion when there is nothing recorded —
// which is the normal case for `go run` and for a build from a working tree.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return devVersion
	}

	return info.Main.Version
}
