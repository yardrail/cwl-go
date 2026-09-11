package cwlcore

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// UUID field widths and bit positions for blank node identifiers (RFC 9562).
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

// blankNodeID returns a deterministic "_:<uuid>" identifier for a process that declares none.
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

// appendCanonical appends a deterministic rendering of n to dst.
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
