package cwlexec

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Errors reported while resolving the values that flow along a workflow's edges.
var (
	// ErrUnknownLinkMerge reports a linkMerge method that is neither merge_nested nor
	// merge_flattened.
	ErrUnknownLinkMerge = errors.New("unknown linkMerge method")

	// ErrUnknownPickValue reports a pickValue method that is none of first_non_null,
	// the_only_non_null and all_non_null.
	ErrUnknownPickValue = errors.New("unknown pickValue method")

	// ErrPickValue reports a pickValue that found the wrong number of non-null values: none at
	// all for first_non_null or the_only_non_null, or more than one for the_only_non_null. The
	// specification makes each of those an error rather than a null result, because a sink that
	// asked for exactly one value and got none has not been satisfied.
	ErrPickValue = errors.New("pickValue found no usable value")

	// ErrIncomplete reports a sink read before the step producing it has finished. It is an
	// internal consistency failure — the ready-queue loop only resolves a sink once every source
	// behind it is available — and never a document error.
	ErrIncomplete = errors.New("source value is not available yet")

	// ErrValueFrom reports a step input's valueFrom expression that failed to evaluate. The
	// underlying cwlcore expression sentinel is wrapped, so a caller can still classify it.
	ErrValueFrom = errors.New("step input valueFrom failed")

	// ErrLoadContents reports a loadContents read of a step input that could not be performed
	// at all — the file is gone, or unreadable. A file that is present but too large or not
	// UTF-8 text is reported as [ErrContentsTooLarge] or [ErrContentsNotText] instead.
	ErrLoadContents = errors.New("loadContents: cannot read the file")

	// ErrLoadListing reports a loadListing read of a step input that could not be performed:
	// the directory is gone, or the walk failed part way down.
	ErrLoadListing = errors.New("loadListing: cannot read the directory")
)

// sourceLookup reads the value behind a resolved source identifier, reporting whether the port that
// produces it has finished.
type sourceLookup func(id string) (any, bool)

// sink is one place a value flows into: a step input, or an output of the run itself. Both are
// wired the same way — one or more sources, combined by linkMerge and filtered by pickValue — so
// they resolve through one implementation rather than two that can drift apart.
type sink struct {
	// Name is the sink's short name, used only in error messages.
	Name string

	// LinkMerge is the declared linkMerge method, empty when the document declared none.
	LinkMerge cwlcore.LinkMergeMethod

	// PickValue is the declared pickValue method, empty when the document declared none.
	PickValue cwlcore.PickValueMethod

	// Sources are the resolved identifiers this sink draws from, in document order.
	Sources []string

	// Type is the type the resolved value must inhabit, and is set only for an output parameter
	// of the run itself. A step input leaves it unset: what a step input has to satisfy is the
	// declared type of the *run process's* parameter, which the process is entitled to be lax
	// about — valueFrom may still be about to replace the value outright, and an undeclared step
	// input has no type at all. An output parameter is the last word on a value, so it is the
	// one place a mismatch is certainly a mismatch.
	Type cwlcore.TypeRef

	// StepInput distinguishes the two kinds of sink, and only [resolveInput] sets it: an output
	// parameter of the run itself is the zero value, so a sink built anywhere else is one by
	// construction. It says whether the value resolved here is leaving the engine, which is what
	// decides whether a Directory's listing is completed; see [sink.value].
	StepInput bool
}

// value resolves the sink: it reads every source, merges them, and applies pickValue.
//
// It expects at least one source. A sink with none has no value to resolve — it is absent, and what
// fills it is a default, which is the caller's business rather than the wiring's; both call sites
// therefore check [sink.wired] first.
//
// An output parameter of the run itself is also where a Directory's listing is completed, which is
// the one point in a run where doing so is safe; see [outFillListings] for the whole of that rule.
func (s *sink) value(lookup sourceLookup) (any, error) {
	values := make([]any, 0, len(s.Sources))

	for _, source := range s.Sources {
		value, ready := lookup(source)
		if !ready {
			return nil, fmt.Errorf("%w: %q reads %q", ErrIncomplete, s.Name, source)
		}

		values = append(values, value)
	}

	merged, err := s.merge(values)
	if err != nil {
		return nil, err
	}

	picked, err := s.pick(merged)
	if err != nil {
		return nil, err
	}

	if !s.StepInput {
		outFillListings(picked)
	}

	return picked, s.checkType(picked)
}

// checkType rejects a resolved value that does not inhabit the sink's declared type, for the sinks
// that have one; see [sink.Type].
//
// It is what makes `pickValue: all_non_null` on a non-array output the error the specification says
// it is. That method "will produce a list", so an output declared `type: string` that picks two
// values has produced something its own declaration forbids, and a runner that handed it back
// anyway would be reporting a workflow output the document says cannot exist.
//
// The value is rendered through [cwlcore.ToExpressionValue] first, for the same reason an output binding's
// result is: inside the engine a File is a *cwlcore.File, and the type checker works on the plain
// JSON-shaped objects a CWL type describes.
func (s *sink) checkType(value any) error {
	err := checkValueType(cwlcore.ToExpressionValue(value), s.Type)
	if err != nil {
		return fmt.Errorf("output %q: %w", s.Name, err)
	}

	return nil
}

// wired reports whether the sink draws on any source at all.
func (s *sink) wired() bool {
	return len(s.Sources) > 0
}

// merge combines the source values according to linkMerge.
//
// A single source with no declared linkMerge passes its value through untouched. That is the one
// case the specification singles out — "if merge_nested is specified with a single link, the value
// from the link must be wrapped in a single-item list" — so wrapping happens exactly when the
// document asked for it, or when there is genuinely more than one link to combine.
func (s *sink) merge(values []any) (any, error) {
	if s.LinkMerge == "" && len(values) == 1 {
		return values[0], nil
	}

	switch s.LinkMerge {
	case "", cwlcore.LinkMergeNested:
		return values, nil
	case cwlcore.LinkMergeFlattened:
		return flattenSources(values), nil
	default:
		return nil, fmt.Errorf("%w: %q declares %q", ErrUnknownLinkMerge, s.Name, s.LinkMerge)
	}
}

// flattenSources concatenates array sources and appends non-array sources as single elements,
// which is what merge_flattened specifies.
func flattenSources(values []any) []any {
	flat := make([]any, 0, len(values))

	for _, value := range values {
		items, isArray := value.([]any)
		if !isArray {
			flat = append(flat, value)

			continue
		}

		flat = append(flat, items...)
	}

	return flat
}

// pick applies pickValue to the merged value.
//
// pickValue is defined over "the first level of a list input", so a merged value that is not a list
// — a single unwrapped source — is treated as a one-element list. That keeps a pickValue on a
// single link meaningful rather than silently inert, and leaves the far more common case, a source
// that is already an array (a conditional scatter's gathered output), working on its own elements.
func (s *sink) pick(merged any) (any, error) {
	if s.PickValue == "" {
		return merged, nil
	}

	items, isArray := merged.([]any)
	if !isArray {
		items = []any{merged}
	}

	kept := make([]any, 0, len(items))

	for _, item := range items {
		if item != nil {
			kept = append(kept, item)
		}
	}

	return s.selectPicked(kept)
}

// selectPicked reduces the non-null values kept by pick to the result the declared method calls for.
func (s *sink) selectPicked(kept []any) (any, error) {
	switch s.PickValue {
	case cwlcore.PickAllNonNull:
		return kept, nil
	case cwlcore.PickFirstNonNull:
		if len(kept) == 0 {
			return nil, fmt.Errorf("%w: %q declares first_non_null but every source is null", ErrPickValue, s.Name)
		}

		return kept[0], nil
	case cwlcore.PickTheOnlyNonNull:
		if len(kept) != 1 {
			return nil, fmt.Errorf("%w: %q declares the_only_non_null but %d sources are non-null",
				ErrPickValue, s.Name, len(kept))
		}

		return kept[0], nil
	default:
		return nil, fmt.Errorf("%w: %q declares %q", ErrUnknownPickValue, s.Name, s.PickValue)
	}
}

// resolveInputs builds a step's input object from the values its sources have produced: sources
// followed, linkMerge and pickValue applied, defaults filled in, and the loadContents and
// loadListing reads the declarations ask for performed.
//
// valueFrom is deliberately not applied here. The specification binds its `inputs` to the step's
// input object "after assigning the source values, applying default, and then scattering", so it
// runs once per scatter sub-job — see [applyValueFrom] — not once for the step.
//
// The step-side secondaryFiles requirement is checked once the object is complete; see
// [checkStepSecondaryFiles] for the rule and joborder_secondary.go for why a step requires what the
// top level discovers. This is the right place for it because it is the only one reached by every
// step and by no bare process: a bare process's input object comes from [ParseJobOrder], which is
// the top level by definition, and never through here.
func resolveInputs(step *plannedStep, lookup sourceLookup) (map[string]any, error) {
	ins := step.step.In
	object := make(map[string]any, len(ins)+len(step.defaults))

	for index := range ins {
		in := &ins[index]

		value, err := resolveInput(step, in, lookup)
		if err != nil {
			return nil, err
		}

		object[ShortName(in.ID)] = value
	}

	err := step.pending.fillProcessDefaults(object)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.id, err)
	}

	err = checkStepSecondaryFiles(step.pending.secondary, object, step.eval)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.id, err)
	}

	return object, nil
}

// resolveInput resolves the value of one step input: its sources, then — if those produced nothing
// — its default, and finally whichever read the step's or the run process's declaration asked for.
func resolveInput(step *plannedStep, in *cwlcore.WorkflowStepInput, lookup sourceLookup) (any, error) {
	name := ShortName(in.ID)
	wiring := sink{
		Name:      name,
		LinkMerge: in.LinkMerge,
		PickValue: in.PickValue,
		Sources:   in.Source,
		Type:      cwlcore.TypeRef{},
		StepInput: true,
	}

	var value any

	if wiring.wired() {
		resolved, err := wiring.value(lookup)
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", step.id, err)
		}

		value = resolved
	}

	// A step input's default covers both "no source" and "the source produced null", which is
	// why it is tested against the resolved value rather than against len(Source). The run
	// process's own defaults follow the same rule one step later; see [pendingValues.fillProcessDefaults].
	if value == nil {
		fallback, err := step.pending.stepDefault(name)
		if err != nil {
			return nil, fmt.Errorf("step %q input %q: %w", step.id, name, err)
		}

		value = fallback
	}

	loaded, err := step.pending.load(name, value)
	if err != nil {
		return nil, fmt.Errorf("step %q input %q: %w", step.id, name, err)
	}

	return loaded, nil
}

// projectDeclaredInputs reduces a step's input object to the parameters the process under run:
// declares.
//
// Workflow.yml, WorkflowStepInput: "Only input parameters declared by the target process will be
// passed through at runtime to the process though additional parameters may be specified (for use
// within `valueFrom` expressions for instance) - unconnected or unused parameters do not represent
// an error condition."
//
// So an undeclared step input is legal, and it is legal precisely *because* it is dropped here. It
// exists to be read by a `valueFrom` or a `when`, both of which have already run by the time this is
// called, and it must not reach the process itself: a tool whose arguments read `$(inputs.in2)` for
// a parameter it never declared has to fail on an undefined field. The conformance suite pins both
// halves with the same wiring — pass-unconnected.cwl must succeed, fail-unconnected.cwl must not.
//
// This is the mirror of [projectOutputs], which reduces a handler's output object to the ports the
// step declares in its out list, and it is deliberately only half as thorough: extra parameters are
// dropped, missing ones are not filled in. An input nothing supplied a value for is absent, and
// inventing a null for it would make "not supplied" indistinguishable from "supplied as null" — a
// distinction [applyProcessDefaults] depends on.
func projectDeclaredInputs(step *plannedStep, object map[string]any) map[string]any {
	if !hasUndeclaredInput(step.declaredIn, object) {
		return object
	}

	projected := make(map[string]any, len(step.declaredIn))

	for name, value := range object {
		if step.declaredIn[name] {
			projected[name] = value
		}
	}

	return projected
}

// hasUndeclaredInput reports whether object carries a key the process does not declare.
//
// Testing first is what lets the ordinary case — every key declared, which is every step of every
// well-behaved document — hand back the object it was given instead of copying it once per
// invocation, and a thousand-element scatter is a thousand invocations.
func hasUndeclaredInput(declared map[string]bool, object map[string]any) bool {
	for name := range object {
		if !declared[name] {
			return true
		}
	}

	return false
}

// applyProcessDefaults fills in the defaults declared by the process under run: for the parameters
// the input object supplies no usable value for.
//
// Nullness is the test, not absence. Process.yml, InputParameter.default: "The default value to use
// for this parameter if the parameter is missing from the input object, *or if the value of the
// parameter in the input object is `null`*." The two are one rule, and the same rule a step input's
// own default follows — see [resolveInput]. Testing presence instead is a wrong answer that only
// shows itself one layer in: a workflow input declared `File?` and left unsupplied reaches a step as
// an explicit null under a key that is very much present, and the tool's own default would then
// never apply.
func applyProcessDefaults(defaults, object map[string]any) {
	for name, value := range defaults {
		if object[name] == nil {
			object[name] = value
		}
	}
}

// pendingValues is everything about a step's inputs that the wiring alone does not carry: the
// `default` each input falls back to, the declared type that default must satisfy, the directory a
// relative reference inside one resolves against, and the two reads a declaration can ask for — a
// File's contents, and a Directory's listing.
//
// The two reads are why this exists at all. A step's input object is assembled by the scheduler and
// never passes through job-order loading, so nothing else is in a position to give a value arriving
// from an upstream step the enrichment the same value would have got from a job file.
type pendingValues struct {
	// stepDefaults maps an input short name to the `default` the step's own `in` entry
	// declared, resolved against the workflow document.
	stepDefaults map[string]deferredValue

	// runDefaults maps an input short name to the `default` the process under run: declared,
	// resolved against that process's document.
	runDefaults map[string]deferredValue

	// types maps an input short name to the type the process under run: declares for it. A
	// step input the run process does not declare has no entry, and its zero TypeRef is what
	// makes conversion walk the value without checking it against anything.
	types map[string]cwlcore.TypeRef

	// loadListing maps an input short name to the `loadListing` its own declaration wrote,
	// empty when it wrote none. Read it through [pendingValues.listingFor], which applies the
	// rest of the precedence.
	loadListing map[string]cwlcore.LoadListingEnum

	// loadContents holds the input short names either the step's `in` entry or the run
	// process's parameter asked for a contents read on.
	loadContents map[string]bool

	// stepBase is the directory a relative reference inside a step-level default resolves
	// against: the directory of the workflow document the step is written in.
	stepBase string

	// runBase is the directory a relative reference inside the run process's parameter default
	// resolves against: the directory of that process's own document.
	runBase string

	// secondary are the run process's input declarations as the step-side secondaryFiles check
	// reads them; see [checkStepSecondaryFiles]. It is populated only for a step of a workflow,
	// because the check itself applies only there — a bare process run directly is the top
	// level, where a declared pattern is discovered rather than required.
	secondary []stepSecondaryDecl

	// listingDefault is the LoadListingRequirement in effect, which is the second step of the
	// `loadListing` precedence and so what a declaration setting none inherits.
	listingDefault cwlcore.LoadListingEnum
}

// deferredValue is a `default` already materialized, together with whatever went wrong doing so.
//
// Materializing happens at planning time, where there is no context to observe and no run under way
// to interrupt. Reporting the failure does not: a `default` naming a file that is not there is
// perfectly legal in a document that always supplies a value for that input, so the failure is held
// until the default turns out to be the value the step actually runs with.
type deferredValue struct {
	value any
	err   error
}

// get returns the materialized value, or the failure that stopped it being one.
func (d deferredValue) get() (any, error) {
	return d.value, d.err
}

// newProcessValues collects what the process under run: contributes: the declared type, the
// parameter default, and the loadContents and loadListing requests of each of its inputs. scope
// supplies the LoadListingRequirement those requests fall back to.
//
// It is the whole of what a bare process — one run as the implicit single step, with no workflow
// step around it — has to contribute. A step of a workflow builds on it with [newPendingValues].
func newProcessValues(
	ctx context.Context, run cwlcore.Process, scope *cwlcore.RequirementScope, decls []portDecl,
) *pendingValues {
	base := documentDir(run)
	listing, _ := loadListingDefault(scope)

	pending := &pendingValues{
		stepDefaults:   make(map[string]deferredValue),
		runDefaults:    make(map[string]deferredValue, len(decls)),
		types:          make(map[string]cwlcore.TypeRef, len(decls)),
		loadListing:    make(map[string]cwlcore.LoadListingEnum, len(decls)),
		loadContents:   make(map[string]bool, len(decls)),
		stepBase:       base,
		runBase:        base,
		secondary:      nil,
		listingDefault: listing,
	}

	for index := range decls {
		decl := &decls[index]
		pending.types[decl.Name] = decl.Type
		pending.loadListing[decl.Name] = decl.LoadListing
		pending.loadContents[decl.Name] = decl.LoadContents
	}

	// The defaults are materialized in a second pass because each is converted against its own
	// parameter's type, loadContents and loadListing, which the first pass is what records.
	for index := range decls {
		decl := &decls[index]
		pending.record(ctx, decl.Name, decl.DefaultNode, pending.runBase, pending.runDefaults)
	}

	return pending
}

// newPendingValues extends [newProcessValues] with what the step itself contributes: its own
// per-input defaults and read requests, which sit closer to the value than the run process's and are
// written in the workflow document rather than the run process's.
//
// A step's own requests do not reach the run process's parameter defaults, and cannot: those fill
// parameters the step wired no `in` entry for at all, so there is no step declaration in play.
func newPendingValues(
	ctx context.Context, workflow *cwlcore.Workflow, step *plannedStep, decls []portDecl,
) *pendingValues {
	pending := newProcessValues(ctx, step.run, step.scope, decls)
	pending.stepBase = documentDir(workflow)
	pending.secondary = stepSecondaryDecls(step.run, step.scope)

	ins := step.step.In

	for index := range ins {
		in := &ins[index]
		name := ShortName(in.ID)
		pending.loadListing[name] = cmp.Or(in.LoadListing, pending.loadListing[name])
		pending.loadContents[name] = pending.loadContents[name] || in.LoadContents
	}

	for index := range ins {
		in := &ins[index]
		pending.record(ctx, ShortName(in.ID), in.Default, pending.stepBase, pending.stepDefaults)
	}

	return pending
}

// listingFor resolves the loadListing in effect for one input, which Process.yml gives a three-step
// precedence: the declaration's own setting, then the LoadListingRequirement in scope, then
// no_listing — which is the empty result, and which reads nothing.
func (p *pendingValues) listingFor(name string) cwlcore.LoadListingEnum {
	return cmp.Or(p.loadListing[name], p.listingDefault)
}

// record materializes one declaration's `default` and files it under name, doing nothing at all
// when the declaration wrote none.
func (p *pendingValues) record(
	ctx context.Context, name string, def salad.Node, base string, into map[string]deferredValue,
) {
	if def == nil || salad.IsNull(def) {
		return
	}

	into[name] = p.materialize(ctx, def, base, name)
}

// stepDefault returns the value of the `default` the step's own `in` entry declared for name, and
// nil when it declared none.
func (p *pendingValues) stepDefault(name string) (any, error) {
	return p.stepDefaults[name].get()
}

// fillProcessDefaults fills in the defaults declared by the process under run: for the parameters
// the step's wiring supplied no usable value for.
//
// Nullness is the test, not absence, for the reason [applyProcessDefaults] sets out: Process.yml
// makes "missing from the input object" and "present but null" the same case, and a step that wires
// an unsupplied optional workflow input produces exactly the second.
func (p *pendingValues) fillProcessDefaults(object map[string]any) error {
	for name, pending := range p.runDefaults {
		if object[name] != nil {
			continue
		}

		value, err := pending.get()
		if err != nil {
			return err
		}

		object[name] = value
	}

	return nil
}

// materialize turns a `default` node into the Go value an input object holds.
//
// It runs the job-order loader over the node rather than [salad.ToAny], which is the whole point:
// that is where a File is resolved against its document, given an absolute path, measured and
// checksummed, and — when the parameter asked for it — read. A `default` is a value written in a
// document exactly as a job order's is, so it deserves the same treatment, and giving it a second
// implementation of that treatment is how the two would drift.
//
// The loader is built with no vocabulary and no LoadListingRequirement, which is what makes this a
// value conversion and not a second format-checking pass: a `default` belongs to a parameter whose
// format constraint the handler checks against the input object as a whole, once.
func (p *pendingValues) materialize(ctx context.Context, node salad.Node, base, name string) deferredValue {
	loader := &joLoader{
		vocab:   joVocabulary{namespaces: nil, hasOntology: false},
		log:     nil,
		jobDir:  base,
		docDir:  base,
		listing: "",
	}
	position := &joValueCtx{
		typ:          p.types[name],
		base:         base,
		path:         name,
		format:       nil,
		listing:      p.listingFor(name),
		loadContents: p.loadContents[name],
	}

	value, err := loader.value(ctx, node, position)
	if err != nil {
		return deferredValue{value: nil, err: err}
	}

	return deferredValue{value: value, err: nil}
}

// load gives one resolved input value the reads its declaration asked for: loadContents on a File,
// loadListing on a Directory.
//
// Both are scoped by Process.yml to the value bound to the declaration — "type: File or an array of
// items: File" for loadContents — so an array is read element by element and anything the request
// does not apply to is returned untouched, because a request that finds nothing to do is not an
// error.
//
// This is the enrichment a top-level input gets from job-order loading. A step's input object never
// passes through that, which is why it has to happen here as well.
func (p *pendingValues) load(name string, value any) (any, error) {
	if !p.loadContents[name] && !readsListing(p.listingFor(name)) {
		return value, nil
	}

	items, isArray := value.([]any)
	if !isArray {
		return p.loadOne(name, value)
	}

	loaded := make([]any, 0, len(items))

	for _, item := range items {
		read, err := p.loadOne(name, item)
		if err != nil {
			return nil, err
		}

		loaded = append(loaded, read)
	}

	return loaded, nil
}

// loadOne applies whichever read the value's own class calls for.
func (p *pendingValues) loadOne(name string, value any) (any, error) {
	switch typed := value.(type) {
	case *cwlcore.File:
		if !p.loadContents[name] {
			return value, nil
		}

		return loadFileContents(typed)
	case *cwlcore.Directory:
		return loadDirectoryListing(typed, p.listingFor(name))
	default:
		return value, nil
	}
}

// readsListing reports whether a resolved loadListing setting asks for the listing to be read at
// all. The empty setting is no_listing, which Process.yml makes the default.
func readsListing(mode cwlcore.LoadListingEnum) bool {
	return mode != "" && mode != cwlcore.LoadListingNone
}

// loadDirectoryListing returns dir with its listing read to the depth loadListing asks for.
//
// A nil Listing that stays nil is the point of the guard, not an oversight: nil records that nothing
// read the listing, which is what `$(inputs.d.listing === undefined)` observes, whereas an empty
// slice asserts that the directory has no entries. A listing the document supplied explicitly is
// never overwritten, and a Directory naming a resource with no local path has nothing to read.
//
// The walk itself is the one the output collector uses. An input Directory and an output Directory
// are the same value read from the same filesystem, and a second walk would be a second chance to
// disagree about entry order, about what a listed File's size and checksum are, or about a symlink
// pointing back up the tree.
func loadDirectoryListing(dir *cwlcore.Directory, mode cwlcore.LoadListingEnum) (any, error) {
	if !readsListing(mode) || dir.Listing != nil || dir.Path == "" {
		return dir, nil
	}

	info, err := os.Stat(dir.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrLoadListing, dir.Path, err)
	}

	listed, err := outCollectDirectory(dir.Path, info, mode)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrLoadListing, dir.Path, err)
	}

	// The Directory is copied for the same reason a File is; see [loadFileContents].
	loaded := *dir
	loaded.Listing = listed.Listing

	return &loaded, nil
}

// loadFileContents returns value with its contents populated, when value is a File that has bytes
// on disk still to read.
//
// The read goes through the same digest the output collector uses, so the 64 KiB ceiling and the
// UTF-8 requirement are enforced in one place and nothing is ever truncated: Process.yml says "if
// the size of the file is greater than 64 KiB, the implementation must raise a fatal error".
//
// The File is copied before its contents are set. The value on a step input is the very object an
// upstream step published, and writing to it would make one step's loadContents request silently
// change what every other consumer of that output sees.
func loadFileContents(file *cwlcore.File) (any, error) {
	if file.Contents.IsSet() || file.Path == "" {
		return file, nil
	}

	stats, err := outDigest(file.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrLoadContents, file.Path, err)
	}

	loaded := *file

	read, err := outWithContents(&loaded, &stats)
	if err != nil {
		return nil, err
	}

	return read, nil
}

// documentDir is the directory a relative location written inside p's document resolves against.
//
// A process carrying no document identifier — one built in memory, or one decoding gave a blank
// node — has no document for such a reference to resolve against, so it falls back to ".": the
// reference then resolves against the process working directory, which is the base the invocation
// itself was written against and the only one still available.
func documentDir(p cwlcore.Process) string {
	return joProcessDir(p, ".")
}

// applyValueFrom returns a copy of one sub-job's input object with every declared valueFrom
// evaluated.
//
// `self` is bound to that input's own incoming value and `inputs` to the whole pre-valueFrom
// object, so that — as the specification requires — "the result of evaluating valueFrom on a
// parameter must not be visible to evaluation of valueFrom on other parameters", whatever order
// they happen to be evaluated in.
func applyValueFrom(step *plannedStep, object map[string]any) (map[string]any, error) {
	if step.implicit {
		return object, nil
	}

	resolved := make(map[string]any, len(object))
	maps.Copy(resolved, object)

	ins := step.step.In

	for index := range ins {
		in := &ins[index]
		if in.ValueFrom == "" {
			continue
		}

		name := ShortName(in.ID)

		value, err := step.eval.Eval(
			string(in.ValueFrom),
			&cwlcore.EvalContext{
				Inputs: object,
				Self:   object[name],
				Runtime: cwlcore.RuntimeContext{
					Cores:      nil,
					RAM:        nil,
					OutdirSize: nil,
					TmpdirSize: nil,
					ExitCode:   nil,
					Outdir:     "",
					Tmpdir:     "",
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("%w: step %q input %q: %w", ErrValueFrom, step.id, name, err)
		}

		resolved[name] = value
	}

	return resolved, nil
}
