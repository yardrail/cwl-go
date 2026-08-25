// Package salad implements a generic Schema Salad (https://www.commonwl.org/v1.2/SchemaSalad.html)
// engine: document loading with $import/$include resolution, jsonldPredicate-driven
// context/vocab resolution, extends/specialize flattening into a typed schema graph, and
// validation of instance documents against that graph.
//
// salad has no knowledge of CWL itself — it can load, flatten, and validate documents
// against any Schema Salad-defined schema. github.com/yardrail/cwl-go/pkg/cwlcore builds
// the CWL v1.2 typed object model on top of it.
package salad
