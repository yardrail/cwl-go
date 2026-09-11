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
	path      string         // corpus-relative path
	err       error          // nil if loaded cleanly
	entry     *manifestEntry // manifest metadata, or nil
	graphOnly bool           // loaded only as $graph (no entry point)
}

// ok reports whether the document loaded.
func (r docResult) ok() bool {
	return r.err == nil
}

// expectedInvalid reports whether all referencing tests expect failure.
func (r docResult) expectedInvalid() bool {
	return r.entry != nil && r.entry.alwaysFails
}

// sweep is the whole Stage 0 result: one docResult per document, in corpus order.
type sweep struct {
	tag     string      // corpus release tag
	root    string      // corpus directory
	results []docResult // one per document, sorted by path
	passed  int
	failed  int
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

// run loads every corpus document in parallel and tallies the outcome.
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
	out := docResult{path: rel, err: nil, entry: m[rel], graphOnly: false}

	abs, err := filepath.Abs(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		out.err = err

		return out
	}

	_, err = cwlcore.LoadFile(ctx, abs, cwlcore.Strict(true))
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

// decodeWholeGraph retries a failed load via DecodeAll for $graph documents without #main.
func decodeWholeGraph(ctx context.Context, abs string) bool {
	doc, err := cwlcore.LoadFileDocument(ctx, abs, cwlcore.Strict(true))
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
	s := &sweep{tag: c.tag, root: c.root, results: results, passed: 0, failed: 0}

	for _, r := range results {
		if r.ok() {
			s.passed++

			continue
		}

		s.failed++
	}

	return s
}

// workerCount picks a worker pool size capped by GOMAXPROCS and item count.
func workerCount(items int) int {
	return max(min(runtime.GOMAXPROCS(0), items), 1)
}
