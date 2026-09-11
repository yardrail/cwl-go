package cwlexec

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Rendering sorted leaf bindings into command-line elements (spec step 5).
// Rules dispatch on the effective value's type, not the schema's.

const (
	// argRadix is the radix a number is written in on a command line.
	argRadix = 10

	// exactIntegerLimit is the largest float64 that faithfully represents every integer below it.
	exactIntegerLimit = 1 << 53

	// fileClassField and filePathField are the map-form keys of a File or Directory.
	fileClassField = "class"
	filePathField  = "path"
)

// fileLike is a File or Directory value viewed uniformly, whichever Go shape it arrived in.
type fileLike struct {
	class string // ClassFile or ClassDirectory.
	path  string // The filesystem path used on the command line.
}

// renderArg converts one sorted leaf binding into the command-line elements it contributes.
func renderArg(bound *boundArg) ([]Arg, error) {
	binding := bound.binding

	// separate=false without a prefix is meaningless.
	if binding.Prefix == "" && binding.Separate.IsSet() && !binding.Separate.Bool() {
		return nil, fmt.Errorf("%w: separate is false but no prefix is declared", ErrBindingPrefix)
	}

	if list, ok := valueList(bound.value); ok {
		return renderList(bound, list)
	}

	return renderScalar(bound)
}

// renderList applies the array binding rule: itemSeparator joins, otherwise prefix only.
// A valueFrom-produced list emits each element as a separate argument.
func renderList(bound *boundArg, list []any) ([]Arg, error) {
	binding := bound.binding

	if len(list) == 0 {
		return nil, nil
	}

	if binding.ItemSeparator != "" {
		joined, err := joinItems(list, binding.ItemSeparator)
		if err != nil {
			return nil, err
		}

		return emitValue(binding, joined), nil
	}

	if bound.computed {
		return renderComputedList(binding, list)
	}

	return prefixOnly(binding), nil
}

// renderComputedList renders a valueFrom list: prefix once, then one arg per element.
func renderComputedList(binding *cwlcore.CommandLineBinding, list []any) ([]Arg, error) {
	args := prefixOnly(binding)

	for _, item := range list {
		text, err := argText(item)
		if err != nil {
			return nil, err
		}

		args = append(args, Arg{Value: text, Quote: shellQuotable(binding)})
	}

	return args, nil
}

// renderScalar renders a non-array value to command-line arguments.
func renderScalar(bound *boundArg) ([]Arg, error) {
	if bound.value == nil {
		return nil, nil
	}

	if flag, ok := bound.value.(bool); ok {
		if !flag {
			return nil, nil
		}

		return renderTrue(bound.binding), nil
	}

	// Record: prefix only; fields were already walked during collection.
	if isRecordValue(bound.value) {
		return prefixOnly(bound.binding), nil
	}

	text, err := argText(bound.value)
	if err != nil {
		return nil, err
	}

	return emitValue(bound.binding, text), nil
}

// renderTrue emits the prefix for a true boolean. No prefix means no output.
func renderTrue(binding *cwlcore.CommandLineBinding) []Arg {
	return prefixOnly(binding)
}

// emitValue renders prefix + value, respecting the `separate` flag.
func emitValue(binding *cwlcore.CommandLineBinding, text string) []Arg {
	quote := shellQuotable(binding)

	if binding.Prefix == "" {
		return []Arg{{Value: text, Quote: quote}}
	}

	if binding.Separate.Or(true) {
		return []Arg{{Value: binding.Prefix, Quote: quote}, {Value: text, Quote: quote}}
	}

	return []Arg{{Value: binding.Prefix + text, Quote: quote}}
}

// prefixOnly renders just a binding's prefix, or nothing when it has none.
func prefixOnly(binding *cwlcore.CommandLineBinding) []Arg {
	if binding.Prefix == "" {
		return nil
	}

	return []Arg{{Value: binding.Prefix, Quote: shellQuotable(binding)}}
}

// shellQuotable reports a binding's effective shellQuote, whose schema default is true.
func shellQuotable(binding *cwlcore.CommandLineBinding) bool {
	return binding.ShellQuote.Or(true)
}

// joinItems renders every element of a list and joins them with separator, for itemSeparator.
func joinItems(list []any, separator string) (string, error) {
	parts := make([]string, 0, len(list))

	for _, item := range list {
		text, err := argText(item)
		if err != nil {
			return "", err
		}

		parts = append(parts, text)
	}

	return strings.Join(parts, separator), nil
}

// argText renders one value as command-line text. File/Directory uses path, not location.
func argText(value any) (string, error) {
	if object, ok := asFileLike(value); ok {
		if object.path == "" {
			return "", fmt.Errorf("%w: %s value has no path", ErrBindingValue, object.class)
		}

		return object.path, nil
	}

	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	default:
	}

	if text, ok := numberText(value); ok {
		return text, nil
	}

	return "", fmt.Errorf("%w: %s has no command line form", ErrBindingValue, cwlcore.TypeName(value))
}

// numberText renders a numeric value as its decimal string. Returns false for non-numbers.
func numberText(value any) (string, bool) {
	if literal, ok := value.(salad.Decimal); ok {
		return literal.String(), true
	}

	reflected := reflect.ValueOf(value)

	switch {
	case reflected.CanInt():
		return strconv.FormatInt(reflected.Int(), argRadix), true
	case reflected.CanUint():
		return strconv.FormatUint(reflected.Uint(), argRadix), true
	case reflected.CanFloat():
		return floatText(reflected.Float()), true
	default:
		return "", false
	}
}

// floatText renders a float using [cwlcore.EncodeJSON] so command-line and interpolation agree.
func floatText(value float64) string {
	return cwlcore.EncodeJSON(value)
}

// integerValue converts value to int64. Returns false for non-integers.
func integerValue(value any) (int64, bool) {
	if literal, ok := value.(salad.Decimal); ok {
		return literal.Int64()
	}

	reflected := reflect.ValueOf(value)

	switch {
	case reflected.CanInt():
		return reflected.Int(), true
	case reflected.CanUint():
		return uintAsInt(reflected.Uint())
	case reflected.CanFloat():
		return floatAsInt(reflected.Float())
	default:
		return 0, false
	}
}

// uintAsInt narrows an unsigned integer, reporting false if it does not fit.
func uintAsInt(value uint64) (int64, bool) {
	if value > math.MaxInt64 {
		return 0, false
	}

	return int64(value), true
}

// floatAsInt narrows a float that is exactly an integer, reporting false otherwise.
func floatAsInt(value float64) (int64, bool) {
	if value != math.Trunc(value) || math.Abs(value) >= exactIntegerLimit {
		return 0, false
	}

	return int64(value), true
}

// valueList views value as a list, accepting []any or any typed slice.
func valueList(value any) ([]any, bool) {
	if list, ok := value.([]any); ok {
		return list, true
	}

	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
		return nil, false
	}

	list := make([]any, reflected.Len())
	for index := range list {
		list[index] = reflected.Index(index).Interface()
	}

	return list, true
}

// valueObject views value as a string-keyed object.
func valueObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)

	return object, ok
}

// isRecordValue reports whether value is an object that is not a File or Directory.
func isRecordValue(value any) bool {
	if _, ok := asFileLike(value); ok {
		return false
	}

	_, ok := valueObject(value)

	return ok
}

// asFileLike views value as a File or Directory, accepting both typed and map forms.
func asFileLike(value any) (fileLike, bool) {
	switch typed := value.(type) {
	case *cwlcore.File:
		return fileLike{class: cwlcore.ClassFile, path: typed.Path}, true
	case *cwlcore.Directory:
		return fileLike{class: cwlcore.ClassDirectory, path: typed.Path}, true
	default:
	}

	object, ok := valueObject(value)
	if !ok {
		return fileLike{class: "", path: ""}, false
	}

	class, ok := object[fileClassField].(string)
	if !ok || (class != cwlcore.ClassFile && class != cwlcore.ClassDirectory) {
		return fileLike{class: "", path: ""}, false
	}

	return fileLike{class: class, path: objectPath(object)}, true
}

// objectPath reads the path out of a File or Directory in map form, or "" when it has none.
func objectPath(object map[string]any) string {
	if path, ok := object[filePathField].(string); ok {
		return path
	}

	return ""
}
