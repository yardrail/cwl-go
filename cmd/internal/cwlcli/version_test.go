package cwlcli

import (
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

func TestVersionTextNamesEveryVersionThatMatters(t *testing.T) {
	t.Parallel()

	got := VersionText("cwl-test")

	for _, want := range []string{"cwl-test", cwlcore.CWLVersionV12, cwlcore.SchemaVersion()} {
		if !strings.Contains(got, want) {
			t.Errorf("VersionText = %q, want it to name %q", got, want)
		}
	}
}
