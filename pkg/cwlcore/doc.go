// Package cwlcore implements the CWL v1.2 (https://www.commonwl.org/v1.2/) typed
// object model: the vendored CWL schema, decoding a validated
// github.com/yardrail/cwl-go/pkg/salad document tree into typed Process/CommandLineTool/
// Workflow/ExpressionTool/Operation structs, requirements/hints scoping, file format
// validation, and CWL expression evaluation.
//
// cwlcore is built on pkg/salad and consumed by pkg/cwlexec, which adds the execution
// engine on top of the typed model.
package cwlcore
