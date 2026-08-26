package salad

import "testing"

// fakeNode is a Node implementation outside the sealed set of *MapNode,
// *SeqNode and *ScalarNode, used to reach appendCanonical's defensive default
// arm.
type fakeNode struct{}

func (fakeNode) Loc() SourceLine { return SourceLine{} }
func (fakeNode) isNode()         {}

func TestAppendCanonicalDefaultsOnAnUnknownNode(t *testing.T) {
	t.Parallel()

	if got := canonicalKey(fakeNode{}); got != "~" {
		t.Errorf("canonicalKey(fakeNode{}) = %q, want %q", got, "~")
	}

	if got := canonicalKey(nil); got != "~" {
		t.Errorf("canonicalKey(nil) = %q, want %q", got, "~")
	}
}
