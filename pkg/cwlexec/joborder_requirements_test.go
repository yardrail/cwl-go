package cwlexec

import (
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// The job-order fragments the merge tests share.
const (
	jobReqEnvName  = "TEST_ENV"
	jobReqOwnValue = "declared_by_the_process"
	jobReqJobValue = "supplied_by_the_job_order"

	jobReqSupplied = `
in: hello
cwl:requirements:
  - class: EnvVarRequirement
    envDef:
      - envName: TEST_ENV
        envValue: supplied_by_the_job_order
`
)

// jobReqTool builds a CommandLineTool declaring one string input and, optionally, an
// EnvVarRequirement of its own, so that a merge can be watched winning or losing against it.
func jobReqTool(own ...cwlcore.ProcessRequirement) *cwlcore.CommandLineTool {
	tool := jobTool(jobParam("in", jobTypeString))
	tool.Requirements = own

	return tool
}

// jobReqEnvVar builds the EnvVarRequirement a process declares for itself.
func jobReqEnvVar(value string) *cwlcore.EnvVarRequirement {
	return &cwlcore.EnvVarRequirement{
		EnvDef: []cwlcore.EnvironmentDef{{EnvName: jobReqEnvName, EnvValue: cwlcore.Expression(value)}},
	}
}

// jobReqEffective resolves the EnvVarRequirement in effect on p, failing when there is none.
func jobReqEffective(t *testing.T, p cwlcore.Process) *cwlcore.EnvVarRequirement {
	t.Helper()

	requirement, found := envVarRequirement(cwlcore.NewScope(p))
	if !found {
		t.Fatal("expected an EnvVarRequirement to be in effect")
	}

	return requirement
}

// jobReqValue reports the value the effective EnvVarRequirement gives the shared variable.
func jobReqValue(t *testing.T, p cwlcore.Process) string {
	t.Helper()

	requirement := jobReqEffective(t, p)
	if len(requirement.EnvDef) != 1 {
		t.Fatalf("expected exactly one envDef entry, got %d", len(requirement.EnvDef))
	}

	if requirement.EnvDef[0].EnvName != jobReqEnvName {
		t.Fatalf("envName: got %q, want %q", requirement.EnvDef[0].EnvName, jobReqEnvName)
	}

	return string(requirement.EnvDef[0].EnvValue)
}

// TestJobOrderRequirementsMergeIntoTheProcess pins the specification's optional input-object merge
// and the precedence the conformance suite demands of it: an entry supplied by the job order beats
// one the process declared for itself.
func TestJobOrderRequirementsMergeIntoTheProcess(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		own  []cwlcore.ProcessRequirement
		want string
	}{
		{
			// tests/env-tool3.cwl + tests/env-job4.yaml: the process declares nothing,
			// so the job order's entry is simply added.
			name: "added to a process that declares none",
			own:  nil,
			want: jobReqJobValue,
		},
		{
			// tests/env-tool4.cwl + tests/env-job3.yaml: the process declares the same
			// variable, and the job order's entry still wins.
			name: "overrides the process's own declaration",
			own:  []cwlcore.ProcessRequirement{jobReqEnvVar(jobReqOwnValue)},
			want: jobReqJobValue,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tool := jobReqTool(tc.own...)
			jobMustParse(t, jobFixtures(t), jobReqSupplied, tool)

			if got := jobReqValue(t, tool); got != tc.want {
				t.Fatalf("effective envValue: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestJobOrderWithoutSuppliedRequirementsLeavesTheProcessAlone checks the two shapes that must not
// merge anything: no cwl:requirements key at all, and one written as an explicit null.
func TestJobOrderWithoutSuppliedRequirementsLeavesTheProcessAlone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  string
	}{
		{name: jobCaseAbsent, src: "in: hello\n"},
		{name: "explicit null", src: "in: hello\ncwl:requirements: null\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tool := jobReqTool(jobReqEnvVar(jobReqOwnValue))
			jobMustParse(t, jobFixtures(t), tc.src, tool)

			if got := len(tool.Requirements); got != 1 {
				t.Fatalf("requirements: got %d, want the process's own 1", got)
			}

			if got := jobReqValue(t, tool); got != jobReqOwnValue {
				t.Fatalf("effective envValue: got %q, want %q", got, jobReqOwnValue)
			}
		})
	}
}

// TestJobOrderRequirementsSuppliedAsAMapping checks that the identifier-map spelling a process
// document may use for both requirements and envDef is accepted from a job order too, since the
// merge decodes the field exactly as a process document's own would be.
func TestJobOrderRequirementsSuppliedAsAMapping(t *testing.T) {
	t.Parallel()

	src := "in: hello\ncwl:requirements:\n  EnvVarRequirement:\n    envDef:\n      TEST_ENV: " +
		jobReqJobValue + "\n"

	tool := jobReqTool()
	jobMustParse(t, jobFixtures(t), src, tool)

	if got := jobReqValue(t, tool); got != jobReqJobValue {
		t.Fatalf("effective envValue: got %q, want %q", got, jobReqJobValue)
	}
}

// TestJobOrderRequirementsOfAnUnknownClassSurviveAsRaw checks that a class this engine does not
// model is carried through as a RawRequirement rather than dropped, so that the capability check
// that rejects an unsupported requirement sees it wherever it was written.
func TestJobOrderRequirementsOfAnUnknownClassSurviveAsRaw(t *testing.T) {
	t.Parallel()

	tool := jobReqTool()
	jobMustParse(t, jobFixtures(t), "in: hello\ncwl:requirements:\n  - class: acme:Nonsense\n", tool)

	if len(tool.Requirements) != 1 {
		t.Fatalf("requirements: got %d, want 1", len(tool.Requirements))
	}

	raw, ok := tool.Requirements[0].(*cwlcore.RawRequirement)
	if !ok {
		t.Fatalf("expected a *cwlcore.RawRequirement, got %T", tool.Requirements[0])
	}

	if raw.ClassIRI != "acme:Nonsense" {
		t.Fatalf("class: got %q, want %q", raw.ClassIRI, "acme:Nonsense")
	}
}

// TestJobOrderRejectsMalformedSuppliedRequirements pins the error path: a cwl:requirements that is
// not a list of requirement objects fails the load, rather than being ignored.
func TestJobOrderRejectsMalformedSuppliedRequirements(t *testing.T) {
	t.Parallel()

	_, err := jobParse(t, jobFixtures(t), "in: hello\ncwl:requirements: 3\n", jobReqTool())
	if err == nil {
		t.Fatal("expected a malformed cwl:requirements to be rejected")
	}

	if got := jobPretty(t, err); !strings.Contains(got, "must be a sequence") {
		t.Fatalf("error does not explain the shape:\n%s", got)
	}
}

// TestMergeRequirementsIgnoresANonMapping checks the guard that lets a job order which is not a
// mapping at all reach the diagnostic that names it as such, instead of failing here first.
func TestMergeRequirementsIgnoresANonMapping(t *testing.T) {
	t.Parallel()

	tool := jobReqTool()

	err := joMergeRequirements(salad.NewSeqNode(salad.SourceLine{}, nil), tool)
	if err != nil {
		t.Fatalf("expected a sequence to be left to the loader, got %v", err)
	}

	if len(tool.Requirements) != 0 {
		t.Fatalf("requirements: got %d, want 0", len(tool.Requirements))
	}
}
