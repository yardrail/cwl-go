// Command cwl-inspect parses a CWL document and dumps its resolved intermediate
// representation, for debugging pkg/salad and pkg/cwlcore.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "cwl-inspect: not yet implemented")
	os.Exit(1)
}
