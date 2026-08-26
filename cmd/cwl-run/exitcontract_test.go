package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlexec"
	"github.com/yardrail/cwl-go/pkg/cwlexec/conformance"
)

// errStandIn stands in for a failure that is not about a version or a feature: an invalid
// document, a job order that does not fit it, a step that exited non-zero.
var errStandIn = errors.New("the document is invalid")

// TestExitStatusMatchesTheInProcessDriver pins the one thing the project's two conformance
// harnesses must agree about.
//
// cwltest drives this binary and reads its exit status; the in-process driver in
// pkg/cwlexec/conformance never forks, so it derives the status a run would have exited
// with and judges from that. The two harnesses are only comparable while the two mappings
// are the same one, and the driver cannot simply call this command's -- cmd sits above
// pkg/cwlexec in the layering, so the dependency only runs this way.
//
// So the definition lives in the driver, this command keeps its own switch for the usage
// status no document can provoke, and this asserts they answer alike for everything a run
// can produce. If they ever drift, the sets TestInProcessMatchesCwltest compares drift
// with them, and this says which half moved.
func TestExitStatusMatchesTheInProcessDriver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "an invalid document", err: errStandIn, want: conformance.ExitFailure},
		{
			name: "an unsupported feature",
			err:  fmt.Errorf("%w: DockerRequirement", cwlexec.ErrUnsupportedFeature),
			want: conformance.ExitUnsupported,
		},
		{
			name: "an unsupported feature joined with another error",
			err:  errors.Join(errStandIn, cwlexec.ErrUnsupportedFeature),
			want: conformance.ExitUnsupported,
		},
		{name: "a run that ended with no explanation", err: errRun, want: conformance.ExitFailure},
		{name: "a suspended run", err: errSuspended, want: conformance.ExitFailure},
		{name: "a document with no cwlVersion", err: errNoCWLVersion, want: conformance.ExitFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			command := exitStatus(tt.err)
			driver := conformance.ExitStatus(tt.err)

			if command != driver {
				t.Errorf("cwl-run exits %d and the in-process driver expects %d", command, driver)
			}

			if command != tt.want {
				t.Errorf("exitStatus = %d, want %d", command, tt.want)
			}
		})
	}
}

// TestExitStatusKeepsUsageToItself records the one status the two do not share.
//
// A mistyped command line is the caller's mistake, not the document's, and cwl-run reports
// it apart from an ordinary failure. No document can provoke it -- the in-process driver is
// handed documents, not arguments -- so the driver has no mapping for it and answers with
// the ordinary failure status instead.
func TestExitStatusKeepsUsageToItself(t *testing.T) {
	t.Parallel()

	if exitStatus(errUsage) != exitUsage {
		t.Errorf("exitStatus(errUsage) = %d, want %d", exitStatus(errUsage), exitUsage)
	}

	if conformance.ExitStatus(errUsage) != conformance.ExitFailure {
		t.Errorf("the driver maps a usage error to %d, want %d",
			conformance.ExitStatus(errUsage), conformance.ExitFailure)
	}
}
