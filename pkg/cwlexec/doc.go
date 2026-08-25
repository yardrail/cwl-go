// Package cwlexec implements the CWL execution engine: a reactive ready-queue scheduler,
// scatter (dotproduct / nested_crossproduct / flat_crossproduct) expansion, when:
// conditional-skip propagation, and the StepHandler registry that dispatches execution by
// a step's `class:` value.
//
// cwlexec is built on github.com/yardrail/cwl-go/pkg/cwlcore's typed model and never
// hardcodes non-core CWL types — unrecognized-but-schema-valid classes are the extension
// point for engines built on top of cwl-go.
package cwlexec
