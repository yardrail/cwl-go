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

// Rendering a sorted leaf binding into command-line elements: step 5 of the specification's
// algorithm, "in the sorted order, apply the rules defined in CommandLineBinding to convert
// bindings to actual command line elements".
//
// Those rules are stated per data type, and this file is one small function per type. The type in
// question is the *value's*, not the schema's, per CommandLineBinding: "if there is a mismatch
// between the type described by the input schema and the effective value ... an implementation
// must use the data type of the effective value".

const (
	// argRadix is the radix a number is written in on a command line.
	argRadix = 10

	// exactIntegerLimit is the largest magnitude a float64 represents every integer below, so
	// beyond it a float is no longer a faithful integer position.
	exactIntegerLimit = 1 << 53

	// fileClassField and filePathField are the fields of a File or Directory value in its
	// decoded map form.
	fileClassField = "class"
	filePathField  = "path"
)

// fileLike is a File or Directory value viewed uniformly, whichever Go shape it arrived in.
type fileLike struct {
	// class is ClassFile or ClassDirectory, for diagnostics.
	class string

	// path is the value's path, which is what a command line names it by.
	path string
}

// renderArg converts one sorted leaf binding into the command-line elements it contributes.
func renderArg(bound *boundArg) ([]Arg, error) {
	binding := bound.binding

	// The specification defines `separate` purely as how a prefix and its value are joined, so
	// declaring it false without a prefix says nothing. The reference implementation rejects it
	// too. An absent `separate` defaults to true and never reaches this.
	if binding.Prefix == "" && binding.Separate.IsSet() && !binding.Separate.Bool() {
		return nil, fmt.Errorf("%w: separate is false but no prefix is declared", ErrBindingPrefix)
	}

	if list, ok := valueList(bound.value); ok {
		return renderList(bound, list)
	}

	return renderScalar(bound)
}

// renderList applies the array rule: "if itemSeparator is specified, add prefix and the join the
// array into a single string with itemSeparator separating the items. Otherwise, first add prefix,
// then recursively process individual elements. If the array is empty, it does not add anything to
// command line."
//
// The recursion into individual elements has already happened during collection, so here the
// non-itemSeparator case adds the prefix and nothing else — except for a list that valueFrom
// produced, which has no element bindings to have recursed into. That case follows the reference
// implementation: the prefix, then every element as its own argument.
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

// renderComputedList renders a list that valueFrom produced: the prefix once, then one argument
// per element. `separate` has nothing to join here, so it does not apply.
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

// renderScalar applies the rules for every value that is not an array: null adds nothing, a
// boolean adds its prefix or nothing at all, a record adds its prefix only, and everything else
// adds its prefix and its string form.
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

	// "object: Add prefix only, and recursively add object fields for which inputBinding is
	// specified." The fields were walked during collection; only the prefix is left.
	if isRecordValue(bound.value) {
		return prefixOnly(bound.binding), nil
	}

	text, err := argText(bound.value)
	if err != nil {
		return nil, err
	}

	return emitValue(bound.binding, text), nil
}

// renderTrue applies the true half of the boolean rule, "if true, add prefix to the command line";
// the false half, "if false, add nothing", is handled at the one call site because a boolean
// parameter deciding which half runs would be a control flag.
//
// A true value with no prefix therefore adds nothing, exactly as a false one does. The
// specification does not say what a prefixless true renders as, and the conformance suite settles
// it: `booleanflags_cl_noinputbinding` — a `required` test — binds `flag: boolean` under
// `inputBinding: {}`, passes it true, and requires an empty argv.
//
// The deviation, recorded because it was this engine's behaviour until the suite ran: emitting
// nothing makes a true indistinguishable from a false on the command line, so a document that
// forgot its prefix produces a quietly wrong invocation rather than a loud failure. That is a real
// hazard and reporting it was defensible — but the suite is the definition of done, and it requires
// the silent form.
func renderTrue(binding *cwlcore.CommandLineBinding) []Arg {
	return prefixOnly(binding)
}

// emitValue renders a prefix and a value together, applying `separate`: true — the schema default —
// keeps them two arguments, false concatenates them into one. A binding with no prefix contributes
// the value alone.
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

// argText renders one value as the text of a command-line argument.
//
// A File or Directory renders as its path, never its location: the binding rules say "add prefix
// and the value of File.path", and a location is a URI in the workflow runner's namespace, not a
// name the tool can open. A value with no path is an error rather than an empty argument, because
// the path is assigned when the file is staged and a command line naming an unstaged file is
// wrong, not merely incomplete.
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

// numberText renders any Go numeric type as the "decimal representation" the binding rules call
// for. ok is false for everything that is not a number.
//
// A number a document wrote arrives as a [salad.Decimal] and is rendered from its own digits, which
// is what the reference implementation does and what this rule means by "the decimal
// representation": Builder.tostr puts a document's float on a command line through Python's
// decimal.Decimal, so 1.23e-05 is written 0.0000123 and an integer literal declared as a float is
// written without a ".0". Only a computed number reaches the reflective path below.
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

// floatText renders a computed float onto a command line, which is the same rendering
// [cwlcore.EncodeJSON] gives it and is deliberately not a second copy of that rule.
//
// The two agree because the reference implementation makes them agree: it puts a number on a
// command line with Python's str() and into an interpolated string with json.dumps, and for a float
// those are the same function — json.dumps writes a float through its repr, and in Python 3 str and
// repr of a float are identical.
//
// Keeping a private copy here is what actually goes wrong in practice: the copy was byte-identical
// to cwlcore's until cwlcore's changed, and then a `double` input carrying an inputBinding put
// "1e+42" on a command line while the same value interpolated into a string said
// "1000000000000000000000000000000000000000000". One rule, one implementation.
func floatText(value float64) string {
	return cwlcore.EncodeJSON(value)
}

// integerValue views value as an int64, for a `position` expression's result. ok is false for
// anything that is not an integer, including a float with a fractional part and one too large for
// an int64 to hold faithfully.
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

// valueList views value as a list. []any covers everything a decoded document or an expression
// result produces; the reflective path accepts a caller's []string or []*cwlcore.File too.
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

// isRecordValue reports whether value is an object that is not a File or Directory — that is, one
// the binding rules treat as a record.
func isRecordValue(value any) bool {
	if _, ok := asFileLike(value); ok {
		return false
	}

	_, ok := valueObject(value)

	return ok
}

// asFileLike views value as a File or Directory, in either the typed form cwlcore decodes to or
// the map form a job order carries. Which of the two an input object holds is a seam this package
// does not get to choose, so both are accepted.
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
