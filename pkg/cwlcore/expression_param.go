package cwlcore

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// The CWL v1.2 parameter-reference grammar, spec BNF:
//
//	symbol             ::= {Unicode alphanumeric}+
//	singleq            ::= [' (( {character - {| \ ' \}} ))* ']
//	doubleq            ::= [" (( {character - {| \ " \}} ))* "]
//	index              ::= [ {decimal digit}+ ]
//	segment            ::= . {symbol} | {singleq} | {doubleq} | {index}
//	parameter reference ::= ( {symbol} {segment}* )
//
// The character classes are Unicode, not ASCII: Go's \w would reject the
// perfectly legal identifiers a non-English document uses, so the classes are
// spelled out as \p{L}\p{N}_. The underscore is not in the spec's BNF but is
// in every implementation's reading of "alphanumeric" and in real documents.
var (
	// paramSymbolRe matches the leading symbol of a reference.
	paramSymbolRe = regexp.MustCompile(`^[\p{L}\p{N}_]+`)

	// paramSegmentRe matches one segment: .field, ['key'], ["key"] or [N].
	paramSegmentRe = regexp.MustCompile(
		`^(?:\.[\p{L}\p{N}_]+|\['(?:[^']|\\')+'\]|\["(?:[^"]|\\")+"\]|\[[0-9]+\])`,
	)
)

// nullSymbol is the one symbol that is a literal rather than a lookup: spec,
// "$(null)" evaluates to the null value.
const nullSymbol = "null"

// lengthKey is the field name the spec gives a special meaning on lists.
const lengthKey = "length"

// initialSegments is the segment capacity a typical reference needs.
const initialSegments = 2

// The JSON type vocabulary TypeName renders values in.
const (
	typeNameBoolean = "a boolean"
	typeNameString  = "a string"
	typeNameNumber  = "a number"
	typeNameList    = "a list"
	typeNameObject  = "an object"
)

// paramSegment is one resolved step of a parameter reference. A segment either
// names a field of an object or indexes a list; the two cases are the spec's
// {symbol}/{singleq}/{doubleq} forms and its {index} form.
type paramSegment struct {
	// text is the segment as written, used to build error messages that
	// point at the exact prefix that failed.
	text string

	// key is the field name for a field segment.
	key string

	// index is the list position for an index segment.
	index int

	// field distinguishes the two forms.
	field bool
}

// evalParamRef evaluates body — a fragment of the form "(...)" — as a
// parameter reference.
//
// The failure it reports is the caller's signal about what went wrong, and the
// three cases are kept apart deliberately:
//
//   - ErrNotParameterReference: body does not parse as a parameter reference,
//     or parses but names a symbol outside the parameter context.
//   - ErrExpressionEval: a well-formed reference that does not resolve — a
//     missing key, an out-of-range index, a field read off a scalar.
//
// It never reports ErrJavaScript. Deciding that JavaScript was the missing
// capability needs to know whether the engine was available at all, which is
// evalFragment's business, not this function's.
func evalParamRef(body string, ctx *EvalContext) (any, error) {
	symbol, segments, ok := parseParamRef(body[1 : len(body)-1])
	if !ok {
		return nil, fmt.Errorf("%w: $%s does not parse as one", ErrNotParameterReference, body)
	}

	if symbol == nullSymbol && len(segments) == 0 {
		// Spec: $(null) is the null value. Returned through a variable
		// because a bare "return nil, nil" reads as a missing result.
		var null any

		return null, nil
	}

	root, ok := rootSymbol(symbol, ctx)
	if !ok {
		return nil, fmt.Errorf("%w: $%s names %q, which is not %s, %s or %s",
			ErrNotParameterReference, body, symbol, rootInputs, rootSelf, rootRuntime)
	}

	value, err := evalSegments(symbol, root, segments)
	if err != nil {
		return nil, err
	}

	// A resolved reference is handed back in the shape an expression reads,
	// so that $(inputs.f) is the same object whether the native walker or the
	// JavaScript engine produced it. Anything holding no typed filesystem
	// value passes through untouched.
	return ToExpressionValue(value), nil
}

// hasParamRefSyntax reports whether body — a fragment of the form "(...)" —
// is lexically a parameter reference, whatever it may then resolve to. It is
// what separates a misspelled root symbol, which the grammar accepts, from
// JavaScript, which it does not.
func hasParamRefSyntax(body string) bool {
	_, _, ok := parseParamRef(body[1 : len(body)-1])

	return ok
}

// parseParamRef splits inner — a reference with its enclosing parentheses
// already removed — into its leading symbol and its segments. ok is false if
// inner is not a parameter reference, in which case it is JavaScript or
// nothing at all.
func parseParamRef(inner string) (string, []paramSegment, bool) {
	symbol := paramSymbolRe.FindString(inner)
	if symbol == "" {
		return "", nil, false
	}

	rest := inner[len(symbol):]
	segments := make([]paramSegment, 0, initialSegments)

	for rest != "" {
		text := paramSegmentRe.FindString(rest)
		if text == "" {
			return "", nil, false
		}

		segment, ok := parseSegment(text)
		if !ok {
			return "", nil, false
		}

		segments = append(segments, segment)
		rest = rest[len(text):]
	}

	return symbol, segments, true
}

// parseSegment decodes one matched segment into its field name or index.
func parseSegment(text string) (paramSegment, bool) {
	if strings.HasPrefix(text, ".") {
		return paramSegment{text: text, key: text[1:], index: 0, field: true}, true
	}

	if quoted := text[1]; quoted == '\'' || quoted == '"' {
		// Strip the brackets and the quotes, then undo the one escape the
		// grammar allows inside each quoting style.
		key := text[2 : len(text)-2]
		key = strings.ReplaceAll(key, `\`+string(quoted), string(quoted))

		return paramSegment{text: text, key: key, index: 0, field: true}, true
	}

	index, err := strconv.Atoi(text[1 : len(text)-1])
	if err != nil {
		// A run of digits too long for an int. Not a usable index, so not a
		// parameter reference.
		return paramSegment{text: "", key: "", index: 0, field: false}, false
	}

	return paramSegment{text: text, key: "", index: index, field: false}, true
}

// rootSymbol resolves a reference's leading symbol against the parameter
// context. Unlike cwl-utils, a symbol whose value is null still resolves: the
// spec requires the native and JavaScript paths to agree, and in JavaScript
// self is a defined global holding null rather than an undefined name.
func rootSymbol(symbol string, ctx *EvalContext) (any, bool) {
	switch symbol {
	case rootInputs:
		return ctx.Inputs, true
	case rootSelf:
		return ctx.Self, true
	case rootRuntime:
		return ctx.Runtime.asMap(), true
	default:
		return nil, false
	}
}

// evalSegments walks the segments left to right, growing path as it goes so a
// failure can name the prefix that produced the offending value.
func evalSegments(path string, current any, segments []paramSegment) (any, error) {
	for i, segment := range segments {
		if length, ok := listLength(current, segment, segments[i+1:]); ok {
			return length, nil
		}

		next, err := evalSegment(path, current, segment)
		if err != nil {
			return nil, err
		}

		current = next
		path += segment.text
	}

	return current, nil
}

// listLength implements the spec's .length shorthand on lists. It applies only
// as the final segment, so .length mid-chain stays an ordinary field lookup
// and fails on a list the way any other field would.
func listLength(current any, segment paramSegment, rest []paramSegment) (any, bool) {
	if !segment.field || segment.key != lengthKey || len(rest) != 0 {
		return nil, false
	}

	list, ok := asList(current)
	if !ok {
		return nil, false
	}

	return int64(len(list)), true
}

// evalSegment applies one segment.
func evalSegment(path string, current any, segment paramSegment) (any, error) {
	if segment.field {
		return lookupField(path, current, segment.key)
	}

	return lookupIndex(path, current, segment.index)
}

// lookupField reads a field of an object. Spec: "It is an error if the key
// does not match the required type, or the key is not found or out of range".
func lookupField(path string, current any, key string) (any, error) {
	object, ok := asMap(current)
	if !ok {
		return nil, fmt.Errorf("%w: %s is %s, which has no field %q",
			ErrExpressionEval, path, TypeName(current), key)
	}

	value, ok := object[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s has no field %q", ErrExpressionEval, path, key)
	}

	return value, nil
}

// lookupIndex reads a position of a list.
//
// Every out-of-range index is an error. cwl-utils skips the bounds check when
// the index is zero, so that $(inputs.empty[0]) yields null there; the spec
// says "the key is not found or out of range" is an error without exempting
// zero, and the spec wins.
func lookupIndex(path string, current any, index int) (any, error) {
	list, ok := asList(current)
	if !ok {
		return nil, fmt.Errorf("%w: %s is %s, which cannot be indexed by position",
			ErrExpressionEval, path, TypeName(current))
	}

	if index >= len(list) {
		return nil, fmt.Errorf("%w: %s index %d is out of range, length %d",
			ErrExpressionEval, path, index, len(list))
	}

	return list[index], nil
}

// isJSONNumber reports whether value is one of Go's numeric kinds, all of
// which render as a JSON number.
func isJSONNumber(value any) bool {
	reflected := reflect.ValueOf(value)

	return reflected.CanInt() || reflected.CanUint() || reflected.CanFloat()
}

// asList views value as a list. []any covers everything a decoded document or
// a JavaScript export produces; the reflective path is there so a caller that
// hands us a []string does not fail in a way that looks like a document bug.
func asList(value any) ([]any, bool) {
	if list, ok := value.([]any); ok {
		return list, true
	}

	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
		return nil, false
	}

	list := make([]any, reflected.Len())
	for i := range list {
		list[i] = reflected.Index(i).Interface()
	}

	return list, true
}

// asMap views value as a string-keyed object, with the same reflective
// fallback as asList.
//
// A typed *File or *Directory is viewed through filesystemView, which is what
// makes $(inputs.f.basename) work on the engine's own values and not just on
// documents. Reading it here rather than at the call sites means the JSON
// encoder and TypeName understand them too, since both go through asMap.
func asMap(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		return object, true
	}

	if object, ok := filesystemView(value); ok {
		return object, true
	}

	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Map || reflected.Type().Key().Kind() != reflect.String {
		return nil, false
	}

	object := make(map[string]any, reflected.Len())
	for iter := reflected.MapRange(); iter.Next(); {
		object[iter.Key().String()] = iter.Value().Interface()
	}

	return object, true
}

// TypeName names a value the way a CWL document's author would, using the JSON
// type vocabulary the spec is written in rather than Go's: "a string", "a
// list", "an object", "null".
//
// It is exported because every consumer of an expression result needs the same
// sentence when a field's declared type and its computed value disagree —
// when wants a boolean, outputEval an object, a scatter field a list — and
// each writing its own renderer would give the same mistake several different
// wordings.
//
// The result is a noun phrase with its article, so it reads directly into a
// message: [fmt.Errorf]("%s evaluated to %s", field, TypeName(v)).
func TypeName(value any) string {
	switch value.(type) {
	case nil:
		return nullSymbol
	case bool:
		return typeNameBoolean
	case string:
		return typeNameString
	default:
	}

	if isJSONNumber(value) {
		return typeNameNumber
	}

	if _, ok := asList(value); ok {
		return typeNameList
	}

	if _, ok := asMap(value); ok {
		return typeNameObject
	}

	return "a " + reflect.TypeOf(value).String()
}
