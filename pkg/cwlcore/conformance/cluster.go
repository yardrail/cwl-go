package conformance

import (
	"cmp"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// signatureLeaves is the number of top leaf messages in a clustering signature.
const signatureLeaves = 3

// Normalization patterns: erase URIs, quoted spans, and bare numbers.
var (
	rePath   = regexp.MustCompile(`(?:[a-z][a-z0-9+.-]*://|/)[^\s'"` + "`" + `,;:)\]]+`)
	reQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'|` + "`[^`]*`")
	reNumber = regexp.MustCompile(`\b\d+\b`)
)

// cluster is a group of documents whose load failures share a normalized signature.
type cluster struct {
	// signature is the normalized key the members were grouped by.
	signature string
	// headline is the representative's most telling leaf message, unnormalized.
	headline string
	// members are the failing documents, in corpus order.
	members []docResult
	// tags counts the conformance feature tags carried by the members.
	tags map[string]int
}

// size is the number of documents in the cluster.
func (c *cluster) size() int {
	return len(c.members)
}

// representative is the member whose error the report renders in full.
func (c *cluster) representative() docResult {
	return c.members[0]
}

// topTags returns the cluster's feature tags ordered by how many members carry them.
func (c *cluster) topTags(limit int) []string {
	names := make([]string, 0, len(c.tags))
	for name := range c.tags {
		names = append(names, name)
	}

	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(c.tags[b], c.tags[a]), cmp.Compare(a, b))
	})

	if len(names) > limit {
		names = names[:limit]
	}

	return names
}

// clusterFailures groups failures by normalized error signature, largest cluster first.
func clusterFailures(failures []docResult) []*cluster {
	byKey := make(map[string]*cluster, len(failures))
	order := make([]*cluster, 0, len(failures))

	for _, f := range failures {
		sig := signature(f.err)

		group, seen := byKey[sig.key]
		if !seen {
			group = &cluster{signature: sig.key, headline: sig.headline, members: nil, tags: make(map[string]int)}
			byKey[sig.key] = group
			order = append(order, group)
		}

		group.members = append(group.members, f)
		countTags(group.tags, f.entry)
	}

	slices.SortStableFunc(order, func(a, b *cluster) int {
		return cmp.Or(cmp.Compare(b.size(), a.size()), cmp.Compare(a.signature, b.signature))
	})

	return order
}

// countTags folds a member's feature tags into the cluster's histogram.
func countTags(dst map[string]int, entry *manifestEntry) {
	if entry == nil {
		return
	}

	for _, tag := range entry.tags {
		dst[tag]++
	}
}

// failureSignature is the clustering key and display message for a group of failures.
type failureSignature struct {
	key      string
	headline string
}

// signature reduces an error to its clustering signature.
func signature(err error) failureSignature {
	var se *salad.Error
	if !errors.As(err, &se) {
		msg := err.Error()

		return failureSignature{key: normalize(msg), headline: msg}
	}

	leaves := se.Leaves()
	if len(leaves) == 0 {
		msg := se.Error()

		return failureSignature{key: normalize(msg), headline: msg}
	}

	ranked := rankMessages(leaves)

	keys := make([]string, 0, signatureLeaves+1)
	if se.Msg != "" {
		keys = append(keys, normalize(se.Msg))
	}

	for _, r := range ranked[:min(len(ranked), signatureLeaves)] {
		keys = append(keys, r.key)
	}

	return failureSignature{
		key:      strings.Join(keys, " ~ "),
		headline: headlineOf(se.Msg, ranked[0].msg),
	}
}

// headlineOf joins the root message and the most frequent tip.
func headlineOf(root, tip string) string {
	if root == "" || root == tip {
		return tip
	}

	return root + ": " + tip
}

// rankedMessage is a distinct leaf message with its normalized key and frequency.
type rankedMessage struct {
	msg   string
	key   string
	count int
}

// rankMessages counts distinct normalized leaves, ordered by frequency then key.
func rankMessages(leaves []*salad.Error) []rankedMessage {
	counts := make(map[string]int, len(leaves))
	first := make(map[string]string, len(leaves))
	order := make([]string, 0, len(leaves))

	for _, leaf := range leaves {
		key := normalize(leaf.Msg)

		_, seen := counts[key]
		if !seen {
			first[key] = leaf.Msg
			order = append(order, key)
		}

		counts[key]++
	}

	ranked := make([]rankedMessage, 0, len(order))
	for _, key := range order {
		ranked = append(ranked, rankedMessage{msg: first[key], key: key, count: counts[key]})
	}

	slices.SortFunc(ranked, func(a, b rankedMessage) int {
		return cmp.Or(cmp.Compare(b.count, a.count), cmp.Compare(a.key, b.key))
	})

	return ranked
}

// normalize erases document-specific parts of a message for clustering.
func normalize(msg string) string {
	out := rePath.ReplaceAllString(msg, "<path>")
	out = reQuoted.ReplaceAllString(out, "<name>")
	out = reNumber.ReplaceAllString(out, "<n>")

	return strings.Join(strings.Fields(out), " ")
}
