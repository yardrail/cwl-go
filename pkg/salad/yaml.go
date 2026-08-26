package salad

import (
	"errors"
	"math"
	"regexp"
	"strconv"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// strTagName is the YAML core-schema tag that forces a scalar to be a string.
const strTagName = "!!str"

// The radix and width every recovered numeric literal is parsed with.
const (
	decimalBase    = 10
	bitsPerFloat64 = 64
)

// The YAML 1.2 core schema's own tag-resolution regular expressions for int and
// float, in their base-ten forms. goccy's scanner recognizes a narrower set than
// these — see [coreSchemaNumber] — so they are spelled out here to recover the
// literals it leaves behind. Go's \d is ASCII-only, which is what the schema
// means by a digit.
var (
	coreSchemaInt   = regexp.MustCompile(`^[-+]?\d+$`)
	coreSchemaFloat = regexp.MustCompile(`^[-+]?(\.\d+|\d+(\.\d*)?)([eE][-+]?\d+)?$`)
)

// Parse parses a Schema Salad document from src and returns its Node tree.
//
// Both YAML and JSON are accepted through the same code path: YAML 1.2 is a
// superset of JSON, and the parser records accurate line/column positions for
// JSON-style flow collections, so no separate JSON reader is needed.
//
// name is recorded as the File component of every SourceLine in the result; pass
// the normalized URL the document was fetched from.
//
// YAML anchors and aliases are expanded, including the merge key "<<": keys
// defined by the mapping itself always win over merged keys, and when several
// mappings are merged the earlier ones win, per the YAML merge specification.
// Duplicate keys are rejected, in block, flow and JSON syntax alike. Documents
// containing more than one YAML document are rejected, since Schema Salad has no
// semantics for them; use $graph instead.
func Parse(name string, src []byte) (Node, error) {
	file, err := parser.ParseBytes(src, 0)
	if err != nil {
		return nil, parseError(name, err)
	}

	if len(file.Docs) > 1 {
		return nil, Errorf(
			SourceLine{File: name},
			"a Schema Salad document must contain a single YAML document, found %d",
			len(file.Docs),
		)
	}

	if len(file.Docs) == 0 {
		// Defensive: goccy's parser.ParseBytes has always returned at least
		// one *ast.DocumentNode for every input tried against it, empty byte
		// slices included, so this has no known way to be driven to true.
		return NewNullNode(SourceLine{File: name}), nil
	}

	c := &yamlConverter{
		file:      name,
		anchors:   make(map[string]ast.Node),
		resolving: make(map[string]bool),
	}

	return c.node(file.Docs[0].Body)
}

// parseError converts a goccy parse failure into a *Error, recovering the
// position from the offending token when the parser reports one.
func parseError(name string, err error) *Error {
	var syntax *yaml.SyntaxError
	if errors.As(err, &syntax) {
		return &Error{Msg: syntax.Message, Loc: tokenLoc(name, syntax.Token)}
	}

	// Verified dead against goccy v1.19.2: a duplicate mapping key is always
	// reported as a *yaml.SyntaxError, caught above, never as this distinct
	// error type. Kept for whichever goccy version does use it.
	var dup *yaml.DuplicateKeyError
	if errors.As(err, &dup) {
		return &Error{Msg: dup.Message, Loc: tokenLoc(name, dup.Token)}
	}

	// Unreachable alongside it, for the same reason: every parser.ParseBytes
	// failure this package has been able to produce is a *yaml.SyntaxError.
	return &Error{Msg: err.Error(), Loc: SourceLine{File: name}}
}

// tokenLoc converts a goccy token position into a SourceLine. goccy reports
// 1-based byte offsets; SourceLine.Offset is 0-based.
func tokenLoc(file string, tk *token.Token) SourceLine {
	if tk == nil || tk.Position == nil {
		return SourceLine{File: file}
	}

	pos := Position{
		Line:   tk.Position.Line,
		Column: tk.Position.Column,
		Offset: max(tk.Position.Offset-1, 0),
	}

	return SourceLine{File: file, Start: pos, End: pos}
}

// yamlConverter walks a goccy AST and builds the equivalent Node tree. It is
// single-use: one converter per parsed document.
type yamlConverter struct {
	anchors   map[string]ast.Node
	resolving map[string]bool
	file      string
}

// node converts any AST node, dispatching containers and indirections here and
// leaf values to scalar.
func (c *yamlConverter) node(n ast.Node) (Node, error) {
	switch v := n.(type) {
	case nil:
		return NewNullNode(SourceLine{File: c.file}), nil
	case *ast.DocumentNode:
		return c.node(v.Body)
	case *ast.MappingNode:
		return c.mapping(c.mappingLoc(v), v.Values)
	case *ast.MappingValueNode:
		return c.mapping(c.nodeLoc(v.Key), []*ast.MappingValueNode{v})
	case *ast.SequenceNode:
		return c.sequence(v)
	default:
		return c.indirect(n)
	}
}

// indirect converts the AST nodes that wrap another value: anchors, aliases and
// tags. Anything else is a leaf scalar.
func (c *yamlConverter) indirect(n ast.Node) (Node, error) {
	switch v := n.(type) {
	case *ast.AnchorNode:
		return c.anchor(v)
	case *ast.AliasNode:
		return c.alias(v)
	case *ast.TagNode:
		return c.tagged(v)
	default:
		return c.scalar(n)
	}
}

// mapping converts an ordered list of key/value pairs, expanding merge keys.
func (c *yamlConverter) mapping(loc SourceLine, values []*ast.MappingValueNode) (Node, error) {
	own := c.ownKeys(values)
	seen := make(map[string]bool, len(values))
	entries := make([]MapEntry, 0, len(values))

	for _, mv := range values {
		if isMergeKey(mv.Key) {
			merged, err := c.mergeEntries(mv.Value, own, seen)
			if err != nil {
				return nil, err
			}

			entries = append(entries, merged...)

			continue
		}

		entry, err := c.entry(mv)
		if err != nil {
			return nil, err
		}

		seen[entry.Key] = true
		entries = append(entries, entry)
	}

	return NewMapNode(loc, entries), nil
}

// entry converts a single non-merge key/value pair.
func (c *yamlConverter) entry(mv *ast.MappingValueNode) (MapEntry, error) {
	key, err := c.mapKey(mv.Key)
	if err != nil {
		return MapEntry{}, err
	}

	value, err := c.node(mv.Value)
	if err != nil {
		return MapEntry{}, err
	}

	return MapEntry{Key: key, Value: value}, nil
}

// ownKeys collects the keys the mapping declares directly, which take precedence
// over anything pulled in by a merge key regardless of position.
func (c *yamlConverter) ownKeys(values []*ast.MappingValueNode) map[string]bool {
	own := make(map[string]bool, len(values))
	for _, mv := range values {
		if isMergeKey(mv.Key) {
			continue
		}

		key, err := c.mapKey(mv.Key)
		if err == nil {
			own[key] = true
		}
	}

	return own
}

// mergeEntries expands a "<<" merge key into the entries it contributes, skipping
// keys the mapping owns and keys an earlier merge already supplied.
func (c *yamlConverter) mergeEntries(value ast.Node, own, seen map[string]bool) ([]MapEntry, error) {
	sources, err := c.mergeSources(value)
	if err != nil {
		return nil, err
	}

	out := make([]MapEntry, 0, len(sources))
	for _, src := range sources {
		for key, val := range src.All() {
			if own[key] || seen[key] {
				continue
			}

			seen[key] = true
			out = append(out, MapEntry{Key: key, Value: val})
		}
	}

	return out, nil
}

// mergeSources resolves the value of a merge key into the mappings it names.
func (c *yamlConverter) mergeSources(value ast.Node) ([]*MapNode, error) {
	seq, ok := value.(*ast.SequenceNode)
	if !ok {
		one, err := c.mergeSource(value)
		if err != nil {
			return nil, err
		}

		return []*MapNode{one}, nil
	}

	out := make([]*MapNode, 0, len(seq.Values))
	for _, item := range seq.Values {
		one, err := c.mergeSource(item)
		if err != nil {
			return nil, err
		}

		out = append(out, one)
	}

	return out, nil
}

// mergeSource resolves one merge source, which must be a mapping.
func (c *yamlConverter) mergeSource(value ast.Node) (*MapNode, error) {
	n, err := c.node(value)
	if err != nil {
		return nil, err
	}

	m, ok := AsMap(n)
	if !ok {
		return nil, Errorf(c.nodeLoc(value), "the YAML merge key `<<` requires a mapping, found %s", NodeKind(n))
	}

	return m, nil
}

// sequence converts an ordered list.
func (c *yamlConverter) sequence(v *ast.SequenceNode) (Node, error) {
	items := make([]Node, 0, len(v.Values))
	for _, item := range v.Values {
		n, err := c.node(item)
		if err != nil {
			return nil, err
		}

		items = append(items, n)
	}

	return NewSeqNode(c.nodeLoc(v), items), nil
}

// anchor records the anchored value under its name and converts it in place.
func (c *yamlConverter) anchor(v *ast.AnchorNode) (Node, error) {
	if name := scalarText(v.Name); name != "" {
		c.anchors[name] = v.Value
	}

	return c.node(v.Value)
}

// alias expands a "*name" reference to the value its anchor recorded.
func (c *yamlConverter) alias(v *ast.AliasNode) (Node, error) {
	name := scalarText(v.Value)

	target, ok := c.anchors[name]
	if !ok {
		return nil, Errorf(c.nodeLoc(v), "reference to undefined YAML anchor &%s", name)
	}

	if c.resolving[name] {
		return nil, Errorf(c.nodeLoc(v), "recursive YAML anchor &%s", name)
	}

	c.resolving[name] = true
	defer delete(c.resolving, name)

	return c.node(target)
}

// tagged converts a tagged value. Only the "!!str" core-schema tag changes the
// result; every other tag is transparent, since Schema Salad documents have no
// meaning for YAML tags.
//
// A scalar under an unrecognized tag keeps the text goccy hands back rather than
// being re-resolved by [stringOrNumber]: a tag the application does not know is
// not an instruction to resolve the content against the core schema, so the raw
// presentation is what survives.
func (c *yamlConverter) tagged(v *ast.TagNode) (Node, error) {
	if v.Start != nil && v.Start.Value == strTagName {
		return NewStringNode(c.nodeLoc(v.Value), scalarText(v.Value)), nil
	}

	if s, ok := v.Value.(*ast.StringNode); ok {
		return NewStringNode(c.nodeLoc(s), s.Value), nil
	}

	return c.node(v.Value)
}

// scalar converts the non-numeric leaf values.
func (c *yamlConverter) scalar(n ast.Node) (Node, error) {
	loc := c.nodeLoc(n)
	switch v := n.(type) {
	case *ast.NullNode:
		return NewNullNode(loc), nil
	case *ast.BoolNode:
		return NewBoolNode(loc, v.Value), nil
	case *ast.StringNode:
		return stringOrNumber(loc, v), nil
	case *ast.LiteralNode:
		return NewStringNode(loc, literalText(v)), nil
	default:
		return c.numeric(n, loc)
	}
}

// numeric converts the numeric leaf values.
func (c *yamlConverter) numeric(n ast.Node, loc SourceLine) (Node, error) {
	switch v := n.(type) {
	case *ast.IntegerNode:
		return integerLiteral(loc, v), nil
	case *ast.FloatNode:
		return floatLiteral(loc, v), nil
	default:
		return c.special(n, loc)
	}
}

// integerLiteral converts a goccy integer, keeping the literal it was written as
// so that rendering can reproduce it.
//
// The kind the literal resolves to is checked rather than assumed: goccy's
// integer grammar is wider than the core schema's base-ten one — it takes hex and
// octal too — and a literal [ParseDecimal] does not recognize keeps the value
// goccy already parsed.
func integerLiteral(loc SourceLine, v *ast.IntegerNode) *ScalarNode {
	if value, ok := ParseDecimal(scalarText(v)); ok && !value.IsFloatForm() {
		return NewNumberNode(loc, value)
	}

	return integerNode(loc, v)
}

// floatLiteral converts a goccy float, keeping the literal it was written as.
//
// This is where 1230000 declared as a float keeps its integer spelling and
// 0.00001 keeps its point: the reference implementation renders a document's
// float from the text the document wrote, and a float64 no longer knows it.
func floatLiteral(loc SourceLine, v *ast.FloatNode) *ScalarNode {
	if value, ok := ParseDecimal(scalarText(v)); ok && value.IsFloatForm() {
		return NewNumberNode(loc, value)
	}

	return NewFloatNode(loc, v.Value)
}

// special converts the IEEE-754 special values, and reports anything left over as
// an unsupported node kind.
func (c *yamlConverter) special(n ast.Node, loc SourceLine) (Node, error) {
	switch v := n.(type) {
	case *ast.InfinityNode:
		return NewFloatNode(loc, v.Value), nil
	case *ast.NanNode:
		return NewFloatNode(loc, math.NaN()), nil
	default:
		return nil, Errorf(loc, "unsupported YAML node of type %s", n.Type())
	}
}

// mapKey renders a mapping key as a string. Schema Salad keys are strings, but
// YAML permits any scalar, so numeric and boolean keys are accepted using their
// source text.
func (c *yamlConverter) mapKey(k ast.MapKeyNode) (string, error) {
	switch v := k.(type) {
	case *ast.StringNode:
		return v.Value, nil
	case *ast.LiteralNode:
		return literalText(v), nil
	case *ast.NullNode:
		return nameNull, nil
	case *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode:
		return scalarText(k), nil
	default:
		return "", Errorf(c.nodeLoc(k), "mapping keys must be scalars, found %s", k.Type())
	}
}

// nodeLoc returns the source location of an AST node.
func (c *yamlConverter) nodeLoc(n ast.Node) SourceLine {
	if n == nil {
		return SourceLine{File: c.file}
	}

	return tokenLoc(c.file, n.GetToken())
}

// mappingLoc points a mapping at its first key rather than at the ":" token that
// goccy reports, which reads better in error messages.
func (c *yamlConverter) mappingLoc(v *ast.MappingNode) SourceLine {
	if len(v.Values) > 0 {
		return c.nodeLoc(v.Values[0].Key)
	}

	return c.nodeLoc(v)
}

// isMergeKey reports whether k is the YAML merge key "<<".
func isMergeKey(k ast.MapKeyNode) bool {
	_, ok := k.(*ast.MergeKeyNode)

	return ok
}

// stringOrNumber converts a goccy string leaf, recovering the numbers goccy's
// scanner declines to type.
//
// goccy resolves a plain scalar to a number only when it is an exponent-free
// decimal fitting an int64 or a uint64, or a float written with a decimal point.
// The YAML 1.2 core schema is wider on both counts: "1e40" is a float, and an
// integer literal is an integer whatever its magnitude. Those arrive here as
// plain strings, so they are re-resolved against the core schema's own grammar
// rather than silently reaching a consumer as text.
//
// A quoted scalar is never reconsidered. Quoting is how a document says that a
// value is a string, and honouring that is the difference between 1e40 and
// "1e40" meaning different things, which is what YAML promises.
func stringOrNumber(loc SourceLine, v *ast.StringNode) Node {
	if !isPlainScalar(v) {
		return NewStringNode(loc, v.Value)
	}

	if n, ok := coreSchemaNumber(loc, v.Value); ok {
		return n
	}

	return NewStringNode(loc, v.Value)
}

// isPlainScalar reports whether a string leaf was written unquoted, which is the
// only form whose type the YAML schema resolves from its spelling.
func isPlainScalar(v *ast.StringNode) bool {
	tk := v.GetToken()

	return tk != nil && tk.Type == token.StringType
}

// coreSchemaNumber resolves a plain scalar against the core schema's numeric
// grammar, reporting false for the text that is genuinely a string.
//
// The literal travels with the value. [NewNumberNode] decides the kind from it,
// which is how an integer too large for an int64 keeps every digit and how a
// float written 1.23e-05 can still be written back as 0.0000123 — neither of
// which survives a float64.
//
// A literal the grammar accepts but [ParseDecimal] will not hold — one whose
// exponent would expand into megabytes of digits — falls back to being parsed as
// a float, where it becomes the infinity or the signed zero it always was.
func coreSchemaNumber(loc SourceLine, text string) (*ScalarNode, bool) {
	if !coreSchemaInt.MatchString(text) && !coreSchemaFloat.MatchString(text) {
		return nil, false
	}

	if value, ok := ParseDecimal(text); ok {
		return NewNumberNode(loc, value), true
	}

	return floatNode(loc, text)
}

// floatNode converts a literal the core-schema grammar has already accepted but
// [ParseDecimal] declined to hold exactly.
//
// A magnitude outside float64's range is kept as the saturated infinity — or
// signed zero — that IEEE 754 rounding produces, rather than rejected: the
// document did write a number, and refusing to parse it would put it back where
// it started, as a string. Any other parse failure leaves the text a string.
func floatNode(loc SourceLine, text string) (*ScalarNode, bool) {
	value, err := strconv.ParseFloat(text, bitsPerFloat64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return nil, false
	}

	return NewFloatNode(loc, value), true
}

// integerNode converts a goccy integer, which holds either an int64 or a uint64.
func integerNode(loc SourceLine, v *ast.IntegerNode) *ScalarNode {
	switch n := v.Value.(type) {
	case int64:
		return NewIntNode(loc, n)
	case uint64:
		return fromUint64(n, loc)
	default:
		// goccy only ever produces int64 or uint64; fall back to the raw lexeme.
		return NewStringNode(loc, scalarText(v))
	}
}

// literalText returns the folded content of a block scalar.
func literalText(v *ast.LiteralNode) string {
	if v.Value == nil {
		return ""
	}

	return v.Value.Value
}

// scalarText returns the source text of a scalar AST node.
func scalarText(n ast.Node) string {
	if n == nil {
		return ""
	}

	tk := n.GetToken()
	if tk == nil {
		return ""
	}

	return tk.Value
}
