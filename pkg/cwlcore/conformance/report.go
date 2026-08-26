package conformance

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// How much of a cluster the report renders before it starts summarising.
const (
	maxPrettyLines = 24
	maxNamedPeers  = 6
	maxShownTags   = 5
	percentScale   = 100.0
	// maxStaleVersions sizes the stale-version list; it is a slice capacity, nothing more.
	maxStaleVersions = 4
)

// summary renders the headline counts: the number the sweep exists to produce.
func (s *sweep) summary() string {
	var b strings.Builder

	total := len(s.results)

	fmt.Fprintf(&b, "Stage 0 parse/validate sweep -- cwl-v1.2 %s\n", s.tag)
	fmt.Fprintf(&b, "  documents swept : %d\n", total)
	fmt.Fprintf(&b, "  loaded          : %d (%s)\n", s.passed, percent(s.passed, total))
	fmt.Fprintf(&b, "  failed          : %d (%s)\n", s.failed, percent(s.failed, total))
	fmt.Fprintf(&b, "  of which $graph documents with no entry point: %d\n", s.count(graphOnlyPass))

	writeShouldFailNote(&b, s)
	writeVersionNote(&b, s)

	return b.String()
}

// writeVersionNote reports documents accepted despite declaring a CWL version other than
// v1.2. See docResult.staleVersion for why that is worth a line of its own.
func writeVersionNote(b *strings.Builder, s *sweep) {
	stale := make([]string, 0, maxStaleVersions)

	for _, r := range s.results {
		if r.staleVersion() {
			stale = append(stale, r.path+" ("+r.version+")")
		}
	}

	fmt.Fprintf(b, "  accepted despite declaring a cwlVersion other than %s: %d\n",
		cwlcore.CWLVersionV12, len(stale))

	for _, name := range stale {
		fmt.Fprintf(b, "      %s\n", name)
	}
}

// writeShouldFailNote reports how the sweep treated the documents the manifest only ever
// expects to fail.
//
// This is the fail-open side of the measurement and it belongs next to the headline: a
// document that every test expects to be rejected but which we happily load is a gap in
// the opposite direction from a failure, and the pass count alone hides it. It is a hint
// rather than a verdict, because a should_fail test may pair a perfectly valid document
// with a job that cannot run -- most of them do.
func writeShouldFailNote(b *strings.Builder, s *sweep) {
	expectedInvalid := s.count(docResult.expectedInvalid)
	rejected := s.count(func(r docResult) bool { return r.expectedInvalid() && !r.ok() })

	fmt.Fprintf(b,
		"  documents named only by should_fail tests: %d, of which %d were rejected at load time\n",
		expectedInvalid, rejected)
}

// count returns how many results satisfy pred.
func (s *sweep) count(pred func(docResult) bool) int {
	n := 0

	for _, r := range s.results {
		if pred(r) {
			n++
		}
	}

	return n
}

// graphOnlyPass reports a document that loaded only as a whole graph. See decodeWholeGraph.
func graphOnlyPass(r docResult) bool {
	return r.graphOnly
}

// report renders the full failure report: the summary followed by every cluster,
// largest first.
//
// Every message is rewritten to drop the corpus directory prefix. That path is a cache
// location that differs on every machine, so leaving it in makes the report both wider
// than a terminal and impossible to diff between two runs.
func (s *sweep) report(clusters []*cluster) string {
	var b strings.Builder

	fmt.Fprint(&b, s.summary())

	if len(clusters) == 0 {
		return b.String()
	}

	fmt.Fprintf(&b, "\n%d failure cluster(s), ranked by document count:\n", len(clusters))

	for i, c := range clusters {
		fmt.Fprintln(&b)
		writeCluster(&b, i+1, c, s.trim)
	}

	return b.String()
}

// trim rewrites the corpus directory prefix out of a message, in both its file URL and
// its plain path spelling.
func (s *sweep) trim(text string) string {
	if s.root == "" {
		return text
	}

	slashed := filepath.ToSlash(s.root)
	out := strings.ReplaceAll(text, "file://"+slashed+"/", "")

	return strings.ReplaceAll(out, slashed+"/", "")
}

// writeCluster renders one cluster: its size, its tags, the full error tree of a
// representative member, and the names of the other members.
func writeCluster(b *strings.Builder, rank int, c *cluster, trim func(string) string) {
	rep := c.representative()

	fmt.Fprintf(b, "[%d] %d document(s): %s\n", rank, c.size(), trim(c.headline))

	tags := c.topTags(maxShownTags)
	if len(tags) > 0 {
		fmt.Fprintf(b, "    tags: %s\n", strings.Join(taggedCounts(c, tags), ", "))
	}

	fmt.Fprintf(b, "    representative: %s%s\n", rep.path, expectedNote(rep))
	writeIndented(b, "      ", trim(prettyOf(rep.err)))

	peers := memberNames(c.members[1:])
	if len(peers) > 0 {
		fmt.Fprintf(b, "    also: %s\n", strings.Join(peers, ", "))
	}
}

// taggedCounts renders "tag(n)" for each tag.
func taggedCounts(c *cluster, tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, tag+"("+strconv.Itoa(c.tags[tag])+")")
	}

	return out
}

// expectedNote marks a member that only should_fail tests reference.
func expectedNote(r docResult) string {
	if r.expectedInvalid() {
		return "  [should_fail only]"
	}

	return ""
}

// memberNames lists the remaining members, truncating a long tail to a count.
func memberNames(rest []docResult) []string {
	if len(rest) == 0 {
		return nil
	}

	shown := min(len(rest), maxNamedPeers)

	names := make([]string, 0, shown+1)
	for _, r := range rest[:shown] {
		names = append(names, r.path)
	}

	if len(rest) > shown {
		names = append(names, "... and "+strconv.Itoa(len(rest)-shown)+" more")
	}

	return names
}

// prettyOf renders an error tree, falling back to the flat message for an error that did
// not come from pkg/salad.
func prettyOf(err error) string {
	var se *salad.Error
	if errors.As(err, &se) {
		return se.Pretty()
	}

	return err.Error()
}

// writeIndented writes text with every line prefixed, truncating a very deep tree so one
// pathological union rejection cannot bury the rest of the report.
func writeIndented(b *strings.Builder, prefix, text string) {
	lines := strings.Split(text, "\n")

	shown := min(len(lines), maxPrettyLines)

	for _, line := range lines[:shown] {
		fmt.Fprintf(b, "%s%s\n", prefix, line)
	}

	if len(lines) > shown {
		fmt.Fprintf(b, "%s... %d more line(s)\n", prefix, len(lines)-shown)
	}
}

// tagBreakdown counts how many failing documents carry each feature tag, largest first.
// It is what tells you whether a cluster is confined to one optional feature.
func tagBreakdown(failures []docResult) []string {
	counts := make(map[string]int, len(failures))
	for _, f := range failures {
		countTags(counts, f.entry)
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}

	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})

	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"("+strconv.Itoa(counts[name])+")")
	}

	return out
}

// percent renders n/total as a one-decimal percentage.
func percent(n, total int) string {
	if total == 0 {
		return "n/a"
	}

	return strconv.FormatFloat(percentScale*float64(n)/float64(total), 'f', 1, 64) + "%"
}
