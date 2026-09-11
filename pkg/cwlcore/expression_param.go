package cwlcore

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// CWL v1.2 parameter-reference grammar regexes.
// Unicode character classes (\p{L}\p{N}_), not ASCII \w.
var (
	// paramSymbolRe matches the leading symbol of a reference.
	paramSymbolRe = regexp.MustCompile(`^[\p{L}\p{N}_]+`)

	// paramSegmentRe matches one segment: .field, ['key'], ["key"] or [N].
	paramSegmentRe = regexp.MustCompile(
		`^(?:\.[\p{L}\p{N}_]+|\['(?:[^']|\\')+'\]|\["(?:[^"]|\\")+"\]|\[[0-9]+\])`,
	)
)

// nullSymbol is the literal null: $(null) evaluates to nil.
const nullSymbol = "null"

// lengthKey is the spec's .length shorthand on lists.
const lengthKey = "length"

// initialSegments is the segment capacity a typical reference needs.
const initialSegments = 2

// JSON type vocabulary for [TypeName].
const (
	typeNameBoolean = "a boolean"
	typeNameString  = "a string"
	typeNameNumber  = "a number"
	typeNameList    = "a list"
	typeNameObject  = "an object"
)

// paramSegment is one step of a parameter reference: a field name or list index.
type paramSegment struct {
	// text is the segment as written, for error messages.
	text string

	// key is the field name for a field segment.
	key string

	// index is the list position for an index segment.
	index int

	// field distinguishes the two forms.
	field bool
}

// evalParamRef evaluates body as a parameter reference.
// Returns ErrNotParameterReference for unparseable or unknown root symbols,
// ErrExpressionEval for valid references that don't resolve.
func evalParamRef(body string, ctx *EvalContext) (any, error) {
	symbol, segments, ok := parseParamRef(body[1 : len(body)-1])
	if !ok {
		return nil, fmt.Errorf("%w: $%s does not parse as one", ErrNotParameterReference, body)
	}

	if symbol == nullSymbol && len(segments) == 0 {
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

	return ToExpressionValue(value), nil
}

// hasParamRefSyntax reports whether body is lexically a parameter reference.
func hasParamRefSyntax(body string) bool {
	_, _, ok := parseParamRef(body[1 : len(body)-1])

	return ok
}

// parseParamRef splits a reference (without parens) into symbol and segments.
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
		key := text[2 : len(text)-2]
		key = strings.ReplaceAll(key, `\`+string(quoted), string(quoted))

		return paramSegment{text: text, key: key, index: 0, field: true}, true
	}

	index, err := strconv.Atoi(text[1 : len(text)-1])
	if err != nil {
		return paramSegment{text: "", key: "", index: 0, field: false}, false
	}

	return paramSegment{text: text, key: "", index: index, field: false}, true
}

// rootSymbol resolves a reference's leading symbol against the parameter context.
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

// evalSegments walks segments left to right, resolving each against the current value.
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

// listLength implements .length on lists. Only applies as the final segment.
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

// lookupField reads a field of an object.
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

// isJSONNumber reports whether value is a Go numeric type.
func isJSONNumber(value any) bool {
	reflected := reflect.ValueOf(value)

	return reflected.CanInt() || reflected.CanUint() || reflected.CanFloat()
}

// asList views value as a []any, with a reflective fallback for typed slices.
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

// asMap views value as a string-keyed map.
// Typed *File/*Directory values are converted via filesystemView.
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

// TypeName returns value's JSON type name ("a string", "null", etc.) for error messages.
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
