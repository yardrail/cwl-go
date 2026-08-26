package cwlexec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// The output object a tool wrote for itself.
//
// invocation.md, Output binding: "If the output directory contains a file named 'cwl.output.json',
// that file must be loaded and used as the output object. In this case, the output object should
// still be type-checked against the `outputs` section, but `outputBinding` is ignored."
//
// So this is not an alternative source of values to merge with the globbed ones — it replaces them
// outright, for every declared parameter, whether or not the parameter has a binding.

// OutputJSONFile is the name of the file a CommandLineTool may write into its output directory to
// supply its output object directly, bypassing output binding.
const OutputJSONFile = "cwl.output.json"

// ErrOutputJSON reports a cwl.output.json that exists and cannot be used: one that is not valid
// JSON, that does not hold an object, or that names a path outside the output directory.
var ErrOutputJSON = errors.New("cwl.output.json is not a usable output object")

// LoadOutputJSON reads the output object a tool wrote into its output directory.
//
// A tool that wrote no such file yields an error wrapping [fs.ErrNotExist], which is the ordinary
// case and means the caller should collect the outputs by binding instead. Test for it with
// [errors.Is] rather than treating any failure as absence: a file that is present and unusable is a
// real error, because a tool that tried to report its outputs and failed has not produced no
// outputs, it has produced broken ones.
//
// Values are normalized exactly as a collected output is: relative paths and locations resolve
// against outdir, `path` wins over `location` when both are given, and each File is measured for
// size and checksum unless it supplied them. Every declared output parameter appears in the result,
// including one the file did not mention, whose value is nil.
//
// inputs is the invocation's input object, and is here for one reason: it says which paths outside
// the output directory the tool is nonetheless entitled to name. See
// [outputCollector.checkPublishable].
//
// The file is read whole and no size limit applies to it. The specification's 64 KiB ceiling belongs
// to loadContents — "the file (or each file in the array) must be a UTF-8 text file 64 KiB or
// smaller" is a sentence about the files an *input* or an output binding loads the contents of — and
// an output object is not one of those. A tool listing ten thousand results writes a megabyte here
// legitimately.
func LoadOutputJSON(
	tool *cwlcore.CommandLineTool, outdir string, inputs map[string]any, opts ...OutputJSONOption,
) (map[string]any, error) {
	settings := outputJSONSettings{revmap: nil}
	for _, opt := range opts {
		opt(&settings)
	}

	path := filepath.Join(outdir, OutputJSONFile)

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}

	object, err := decodeOutputJSON(data)
	if err != nil {
		return nil, err
	}

	if settings.revmap != nil {
		revmapObject(object, settings.revmap)
	}

	return bindOutputJSON(newOutputCollector(tool, outdir, inputs), object)
}

// OutputJSONOption adjusts how [LoadOutputJSON] reads the file.
type OutputJSONOption func(*outputJSONSettings)

// outputJSONSettings is what the options set.
type outputJSONSettings struct {
	revmap func(string) string
}

// WithHostPaths reads the file as one written in a namespace other than this host's, mapping every
// path in it back with revmap before anything goes looking for the file it names.
//
// It is what a contained invocation needs. `$(runtime.outdir)` inside a container is the container's
// output directory, so a tool that writes `{"foo": {"path": "$(runtime.outdir)/foo"}}` names a path
// this process does not have — conformance tests docker_json_output_path and
// docker_json_output_location are that document with the two spellings, one giving `path` and the
// other a file:// URI in `location`. This is cwltool's revmap_file, which it applies for the same
// reason and at the same point.
func WithHostPaths(revmap func(string) string) OutputJSONOption {
	return func(settings *outputJSONSettings) { settings.revmap = revmap }
}

// revmapPaths rewrites every `path` and `location` in a decoded output object, leaving the rest of
// it alone. It descends through arrays and nested objects, so a secondaryFiles entry is mapped as
// its File is.
func revmapPaths(value any, revmap func(string) string) any {
	switch typed := value.(type) {
	case map[string]any:
		revmapObject(typed, revmap)

		return typed
	case []any:
		for index, inner := range typed {
			typed[index] = revmapPaths(inner, revmap)
		}

		return typed
	default:
		return value
	}
}

// revmapObject rewrites one object in place, which is the shape the top of an output object always
// has and the reason it is separate: the caller there has a map already and wants it back.
func revmapObject(object map[string]any, revmap func(string) string) {
	for key, inner := range object {
		switch key {
		case outKeyPath:
			object[key] = revmapPath(inner, revmap)
		case outKeyLocation:
			object[key] = revmapLocation(inner, revmap)
		default:
			object[key] = revmapPaths(inner, revmap)
		}
	}
}

// revmapPath maps a `path`, which is a local path or nothing this function can act on. A relative
// one is left alone: it resolves against the output directory, which the collector already reads as
// this host's.
func revmapPath(value any, revmap func(string) string) any {
	text, ok := value.(string)
	if !ok || !filepath.IsAbs(text) {
		return value
	}

	return revmap(text)
}

// fileScheme prefixes the one URI scheme a location may carry a local path in.
const fileScheme = "file://"

// revmapLocation maps a `location`, which may be written either as a local path or as the file://
// URI of one. A location under any other scheme names something that is not on a filesystem at all,
// so there is nothing to map.
func revmapLocation(value any, revmap func(string) string) any {
	text, ok := value.(string)
	if !ok {
		return value
	}

	rest, isFileURI := strings.CutPrefix(text, fileScheme)
	if !isFileURI {
		return revmapPath(value, revmap)
	}

	if !filepath.IsAbs(rest) {
		return value
	}

	return fileScheme + revmap(rest)
}

// decodeOutputJSON parses the file into the object it must hold.
func decodeOutputJSON(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))

	// Numbers are decoded as json.Number and converted afterwards, because the alternative —
	// encoding/json's default of float64 for everything — would turn an integer-typed output
	// written as 3 into 3.0, which is a different CWL value.
	decoder.UseNumber()

	var decoded any

	err := decoder.Decode(&decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOutputJSON, err)
	}

	object, ok := jsonNumbers(decoded).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: it holds %s, want an object", ErrOutputJSON, cwlcore.TypeName(decoded))
	}

	return object, nil
}

// jsonNumbers replaces every [json.Number] a decode produced with the value the rest of the engine
// speaks, keeping an integer an integer.
//
// A [json.Number] is a lexeme, which is exactly what a number needs to survive this hop, so it is
// parsed as a [salad.Decimal] rather than converted: a tool that wrote a forty-three-digit integer
// into cwl.output.json gets forty-three digits back, and one that wrote 0.0000123 does not get
// 1.23e-05. Only an int64 is taken out of the literal, because an integer is an integer however it
// is written and the whole engine reads one as an int64.
//
// The one literal that stays text is the one no exact rendering will expand: encoding/json accepts
// "1e99999999", and a hundred megabytes of digits is not a number anyone wanted. It is out of
// float64's range too, so there is nothing to narrow it to either.
func jsonNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		return jsonNumber(typed)
	case map[string]any:
		for key, member := range typed {
			typed[key] = jsonNumbers(member)
		}

		return typed
	case []any:
		for index, member := range typed {
			typed[index] = jsonNumbers(member)
		}

		return typed
	default:
		return value
	}
}

// jsonNumber converts one JSON number, keeping the literal the tool wrote.
func jsonNumber(number json.Number) any {
	literal, ok := salad.ParseDecimal(number.String())
	if !ok {
		return number.String()
	}

	if !literal.IsFloatForm() {
		if whole, fits := literal.Int64(); fits {
			return whole
		}
	}

	return literal
}

// bindOutputJSON maps the object the tool wrote onto the tool's declared output ports.
//
// Ports the file does not mention are null rather than absent, so that a downstream step wired to
// one never blocks on a key that will not arrive — the same rule collection by binding follows.
// Fields the file names that are not declared ports are dropped: the specification says the object
// "should still be type-checked against the `outputs` section", and a value with no port to travel
// on has nowhere to go.
//
// A value here is finished exactly as a collected one is, and for the same reason — which of the two
// ways a tool reported an output should not change the output. Anything declaring a basename its
// path does not have is moved to that name (see [outputCollector.relocate]), and every Directory is
// given the listing it must be published with (see [outFillListings]).
func bindOutputJSON(collector *outputCollector, object map[string]any) (map[string]any, error) {
	tool := collector.tool
	outputs := make(map[string]any, len(tool.Outputs))

	for index := range tool.Outputs {
		name := ShortName(tool.Outputs[index].ID())

		declared, written := object[name]
		if !written {
			outputs[name] = nil

			continue
		}

		value, err := collector.retypeValue(declared)
		if err != nil {
			return nil, fmt.Errorf("%w: output %q: %w", ErrOutputJSON, name, err)
		}

		err = collector.checkPublishable(value)
		if err != nil {
			return nil, fmt.Errorf("%w: output %q: %w", ErrOutputJSON, name, err)
		}

		err = collector.relocate(value)
		if err != nil {
			return nil, fmt.Errorf("%w: output %q: %w", ErrOutputJSON, name, err)
		}

		outFillListings(value)

		outputs[name] = value
	}

	return outputs, nil
}

// checkPublishable rejects a value naming a path this invocation may not publish.
//
// invocation.md is explicit: "If the value of the 'path' is an absolute path pattern (it does begin
// with a slash '/') then it must refer to a path within the output directory. It is an error for
// 'path' to refer outside the output directory." A tool that invents a path elsewhere on the machine
// is publishing a file the run does not own and cannot promise will still be there.
//
// "Within the output directory" is read as the same set the glob collector may publish from — the
// output directory, or one of the paths the invocation brought into it; see [outAllowedRoots]. A
// tool handed a File and asked to hand it back is returning an input, not escaping:
// `paramref_arguments_roundtrip` echoes `$(inputs.a_record)` straight into cwl.output.json, and the
// record's File names the input where it really lives. Reading the sentence without that allowance
// would make returning your own input an error, which is not what it is about.
func (c *outputCollector) checkPublishable(value any) error {
	if file, ok := value.(*cwlcore.File); ok && file != nil {
		return c.checkPath(file.Path, file.SecondaryFiles)
	}

	if dir, ok := value.(*cwlcore.Directory); ok && dir != nil {
		return c.checkPath(dir.Path, dir.Listing)
	}

	switch typed := value.(type) {
	case []any:
		return c.checkEachPublishable(typed)
	case map[string]any:
		return c.checkFieldsPublishable(typed)
	default:
		return nil
	}
}

// checkPath checks one filesystem value's own path and then its children.
func (c *outputCollector) checkPath(local string, children []cwlcore.FileOrDirectory) error {
	if local != "" && !c.publishable(local) {
		return fmt.Errorf("%w: %q is outside the output directory %q and the inputs %s",
			ErrOutputJSON, local, c.outdir, outQuoted(c.roots))
	}

	for _, child := range children {
		err := c.checkPublishable(child)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkEachPublishable checks every member of a list.
func (c *outputCollector) checkEachPublishable(values []any) error {
	for _, value := range values {
		err := c.checkPublishable(value)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkFieldsPublishable checks every field of a record.
func (c *outputCollector) checkFieldsPublishable(object map[string]any) error {
	for _, value := range object {
		err := c.checkPublishable(value)
		if err != nil {
			return err
		}
	}

	return nil
}
