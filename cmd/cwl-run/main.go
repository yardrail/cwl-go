// Command cwl-run is a cwl-runner-compatible CLI entrypoint: it drives execution of a
// CWL document, following the cwl-runner invocation and exit-code contract
// (https://www.commonwl.org/v1.2/CommandLineTool.html#Executing_CWL_documents_and_tools)
// so it can be exercised by the cwltest conformance harness.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "cwl-run: not yet implemented")
	os.Exit(1)
}
