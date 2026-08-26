package conformance

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// docResult is the outcome of loading one corpus document.
type docResult struct {
	// path is the document's corpus-relative, slash-separated path.
	path string
	// err is nil when the document loaded and decoded cleanly.
	err error
	// entry is what the test manifest says about this document, or nil when no
	// conformance test references it directly.
	entry *manifestEntry
	// graphOnly marks a $graph document that decoded as a whole but names no entry
	// point, so it can only be addressed by fragment. See decodeWholeGraph.
	graphOnly bool
}

// ok reports whether the document loaded.
func (r docResult) ok() bool {
	return r.err == nil
}

// expectedInvalid reports whether every conformance test naming this document expects a
// failure, which is the manifest's strongest available hint that the document itself is
// meant to be rejected. It is a hint and not a verdict: a should_fail test may equally
// well pair a valid document with a job that cannot run.
func (r docResult) expectedInvalid() bool {
	return r.entry != nil && r.entry.alwaysFails
}

// sweep is the whole Stage 0 result: one docResult per document, in corpus order.
type sweep struct {
	// tag is the corpus release the sweep ran against.
	tag string
	// root is the corpus directory, kept so the report can strip it from messages.
	root string
	// results holds one entry per swept document, sorted by path.
	results []docResult
	// passed and failed partition results by outcome.
	passed int
	failed int
}

// failures returns the failing results, in corpus order.
func (s *sweep) failures() []docResult {
	out := make([]docResult, 0, s.failed)

	for _, r := range s.results {
		if !r.ok() {
			out = append(out, r)
		}
	}

	return out
}

// failingPaths returns the corpus-relative paths that failed to load, in corpus order.
func (s *sweep) failingPaths() []string {
	out := make([]string, 0, s.failed)

	for _, r := range s.results {
		if !r.ok() {
			out = append(out, r.path)
		}
	}

	return out
}

// run loads every document in the corpus, in parallel across GOMAXPROCS workers, and
// tallies the outcome.
//
// Each document is loaded exactly as cmd/cwl-run would load it: through
// cwlcore.LoadFile under strict validation, which is salad's $import/$include resolution
// and link checking, schema validation against the embedded schema for the CWL version
// the document declares, the upgrade of anything older into v1.2 form, and decoding into
// the typed model. Nothing is executed.
//
// Strict is what the runner uses, and it is what the reference implementation uses --
// cwltool's LoadingContext.strict defaults to true. It matters here for one reason worth
// naming: v1.0 and v1.1 spell a requirement's class as a plain string rather than a
// single-symbol enum, so under permissive validation a mistyped ResourceRequirement field
// simply matches some other requirement record with an undeclared field, and the document
// that should have been rejected is accepted instead.
func run(ctx context.Context, c *corpus, docs []string, m manifest) *sweep {
	results := make([]docResult, len(docs))

	var wg sync.WaitGroup

	work := make(chan int)

	for range workerCount(len(docs)) {
		wg.Go(func() {
			for i := range work {
				results[i] = loadOne(ctx, c, docs[i], m)
			}
		})
	}

	for i := range docs {
		work <- i
	}

	close(work)
	wg.Wait()

	return tally(c, results)
}

// loadOne loads a single document and pairs the outcome with its manifest entry.
func loadOne(ctx context.Context, c *corpus, rel string, m manifest) docResult {
	out := docResult{path: rel, entry: m[rel]}

	abs, err := filepath.Abs(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		out.err = err

		return out
	}

	_, err = cwlcore.LoadFile(ctx, abs, salad.Strict(true))
	if err == nil {
		return out
	}

	out.err = err
	if decodeWholeGraph(ctx, abs) {
		out.err = nil
		out.graphOnly = true
	}

	return out
}

// decodeWholeGraph retries a failed load as a whole-document decode, reporting whether
// that succeeded.
//
// It exists for one shape: a $graph document that declares no "#main". cwlcore.LoadFile
// rejects those, correctly, because there is no entry point to run -- but the conformance
// manifest addresses them by fragment ("tests/conflict-wf.cwl#collision"), so the document
// itself is perfectly valid. Entry-point selection is not what Stage 0 measures, so the
// document is re-decoded through DecodeAll and counted on its own terms.
//
// The retry is deliberately confined to graph documents and only runs after a failure,
// so the happy path stays a single load through the full stack, external run references
// included.
func decodeWholeGraph(ctx context.Context, abs string) bool {
	doc, err := cwlcore.LoadFileDocument(ctx, abs, salad.Strict(true))
	if err != nil {
		return false
	}

	_, isGraph := salad.AsSeq(doc.Root)
	if !isGraph {
		return false
	}

	_, err = cwlcore.DecodeAll(doc)

	return err == nil
}

// tally counts the outcomes.
func tally(c *corpus, results []docResult) *sweep {
	s := &sweep{tag: c.tag, root: c.root, results: results}

	for _, r := range results {
		if r.ok() {
			s.passed++

			continue
		}

		s.failed++
	}

	return s
}

// workerCount picks a worker pool size: enough to keep the machine busy, never more
// than there is work for.
func workerCount(items int) int {
	return max(min(runtime.GOMAXPROCS(0), items), 1)
}
