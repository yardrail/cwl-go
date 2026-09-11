package conformance

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Report rendering limits.
const (
	maxPrettyLines = 24
	maxNamedPeers  = 6
	maxShownTags   = 5
	percentScale   = 100.0
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

	return b.String()
}

// writeShouldFailNote reports how should_fail-only documents were treated.
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

// report renders the summary followed by every failure cluster.
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

// trim strips the corpus directory prefix from a message.
func (s *sweep) trim(text string) string {
	if s.root == "" {
		return text
	}

	slashed := filepath.ToSlash(s.root)
	out := strings.ReplaceAll(text, "file://"+slashed+"/", "")

	return strings.ReplaceAll(out, slashed+"/", "")
}

// writeCluster renders one cluster with its representative error and members.
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

// prettyOf renders an error tree, falling back to the flat message.
func prettyOf(err error) string {
	if se, ok := errors.AsType[*salad.Error](err); ok {
		return se.Pretty()
	}

	return err.Error()
}

// writeIndented writes text with every line prefixed, truncating after maxPrettyLines.
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

// tagBreakdown counts failing documents per feature tag, largest first.
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
