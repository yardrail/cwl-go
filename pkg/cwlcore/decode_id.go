package cwlcore

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// UUID field widths and the bit positions RFC 9562 reserves for the version and
// variant, used to render a blank node identifier.
const (
	uuidBytes        = 16
	uuidVersionIndex = 6
	uuidVariantIndex = 8
	uuidVersionMask  = 0x0f
	uuidVersionBits  = 0x50
	uuidVariantMask  = 0x3f
	uuidVariantBits  = 0x80
	uuidGroupA       = 8
	uuidGroupB       = 12
	uuidGroupC       = 16
	uuidGroupD       = 20
)

// blankNodeID returns the identifier assigned to a process that declares none.
//
// The schema makes a process's id optional, but a process still has to be
// referable — as a step's run target, as a key in DecodeAll's result — so
// decoding assigns one, in the "_:<uuid>" blank node form the Schema Salad
// identifier rules provide for.
//
// It is deterministic: the identifier is a version-5 UUID over the process
// node's source location followed by a canonical rendering of the node itself.
// Decoding the same document twice therefore produces the same identifiers, so
// test diffs and error messages stay stable. Including the source location is
// what keeps two structurally identical inline processes in one document apart;
// when locations are unknown, as they are for a node built in memory, identical
// processes do collapse onto one identifier.
func blankNodeID(node salad.Node) string {
	seed := append([]byte(nodeLoc(node).String()), 0)
	sum := sha256.Sum256(appendCanonical(seed, node))

	return blankNodePrefix + formatUUID(sum[:uuidBytes])
}

// formatUUID renders 16 bytes as a version-5 UUID string.
func formatUUID(raw []byte) string {
	stamped := make([]byte, uuidBytes)
	copy(stamped, raw)
	stamped[uuidVersionIndex] = stamped[uuidVersionIndex]&uuidVersionMask | uuidVersionBits
	stamped[uuidVariantIndex] = stamped[uuidVariantIndex]&uuidVariantMask | uuidVariantBits

	text := hex.EncodeToString(stamped)

	return strings.Join([]string{
		text[:uuidGroupA],
		text[uuidGroupA:uuidGroupB],
		text[uuidGroupB:uuidGroupC],
		text[uuidGroupC:uuidGroupD],
		text[uuidGroupD:],
	}, "-")
}

// appendCanonical appends a deterministic rendering of n to dst. Key order is
// preserved, and every scalar is tagged with its kind, so that two nodes render
// alike exactly when they hold the same values in the same order.
func appendCanonical(dst []byte, n salad.Node) []byte {
	switch value := n.(type) {
	case *salad.MapNode:
		dst = append(dst, '{')
		for key, item := range value.All() {
			dst = strconv.AppendQuote(dst, key)
			dst = appendCanonical(append(dst, ':'), item)
			dst = append(dst, ',')
		}

		return append(dst, '}')
	case *salad.SeqNode:
		dst = append(dst, '[')
		for _, item := range value.Items() {
			dst = append(appendCanonical(dst, item), ',')
		}

		return append(dst, ']')
	case *salad.ScalarNode:
		dst = append(dst, value.Kind().String()...)

		return append(strconv.AppendQuote(append(dst, '('), value.String()), ')')
	default:
		return append(dst, '~')
	}
}
