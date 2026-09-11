package cwlexec

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Errors reported while resolving the values that flow along a workflow's edges.
var (
	// ErrUnknownLinkMerge reports an unrecognized linkMerge method.
	ErrUnknownLinkMerge = errors.New("unknown linkMerge method")

	// ErrUnknownPickValue reports an unrecognized pickValue method.
	ErrUnknownPickValue = errors.New("unknown pickValue method")

	// ErrPickValue reports a pickValue with the wrong number of non-null values.
	ErrPickValue = errors.New("pickValue found no usable value")

	// ErrIncomplete reports a sink read before its producing step has finished.
	ErrIncomplete = errors.New("source value is not available yet")

	// ErrValueFrom reports a failed valueFrom expression evaluation.
	ErrValueFrom = errors.New("step input valueFrom failed")

	// ErrLoadContents reports an unreadable file during loadContents.
	ErrLoadContents = errors.New("loadContents: cannot read the file")

	// ErrLoadListing reports a failed directory listing read.
	ErrLoadListing = errors.New("loadListing: cannot read the directory")
)

// sourceLookup reads a source value by identifier, reporting whether the port is ready.
type sourceLookup func(id string) (any, bool)

// sink is a step input or workflow output that receives values from one or more sources.
type sink struct {
	// Name is the sink's short name, used only in error messages.
	Name string

	// LinkMerge is the declared linkMerge method, empty when the document declared none.
	LinkMerge cwlcore.LinkMergeMethod

	// PickValue is the declared pickValue method, empty when the document declared none.
	PickValue cwlcore.PickValueMethod

	// Sources are the resolved identifiers this sink draws from, in document order.
	Sources []string

	// Type is set only for workflow output parameters; step inputs leave it unset.
	Type cwlcore.TypeRef

	// StepInput is true for step inputs, false for workflow outputs.
	StepInput bool
}

// value resolves the sink: reads sources, merges, applies pickValue.
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
		outFillListingsLocal(picked)
	}

	return picked, s.checkType(picked)
}

// checkType rejects a resolved value that doesn't match the sink's declared type.
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

// merge combines source values according to linkMerge.
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

// flattenSources implements merge_flattened: concatenates arrays, appends scalars.
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

// pick applies pickValue to the merged value, filtering nulls.
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

// resolveInputs builds a step's input object from source values, defaults, and load reads.
// valueFrom is applied later, per scatter sub-job.
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

// resolveInput resolves one step input: sources, then default, then load reads.
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

// projectDeclaredInputs drops step inputs that the run process doesn't declare.
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

// hasUndeclaredInput reports whether object has a key not in declared.
func hasUndeclaredInput(declared map[string]bool, object map[string]any) bool {
	for name := range object {
		if !declared[name] {
			return true
		}
	}

	return false
}

// applyProcessDefaults fills in defaults for missing or null parameters.
func applyProcessDefaults(defaults, object map[string]any) {
	for name, value := range defaults {
		if object[name] == nil {
			object[name] = value
		}
	}
}

// pendingValues holds defaults, types, and load settings for a step's inputs.
type pendingValues struct {
	// stepDefaults maps input name to the step's own default value.
	stepDefaults map[string]deferredValue

	// runDefaults maps input name to the run process's default value.
	runDefaults map[string]deferredValue

	// types maps input name to the run process's declared type.
	types map[string]cwlcore.TypeRef

	// loadListing maps input name to its declared loadListing, empty if unset.
	loadListing map[string]cwlcore.LoadListingEnum

	// loadContents holds input names that requested contents loading.
	loadContents map[string]bool

	// stepBase is the workflow document directory for resolving step-level defaults.
	stepBase string

	// runBase is the run process document directory for resolving its defaults.
	runBase string

	// secondary holds run process input declarations for the step-side secondaryFiles check.
	secondary []stepSecondaryDecl

	// listingDefault is the process-level LoadListingRequirement fallback.
	listingDefault cwlcore.LoadListingEnum

	// resolver resolves file paths from prior step outputs. Nil means local filesystem.
	resolver OutputResolver
}

// deferredValue is a materialized default value, deferring error reporting until use.
type deferredValue struct {
	value any
	err   error
}

// get returns the materialized value, or the failure that stopped it being one.
func (d deferredValue) get() (any, error) {
	return d.value, d.err
}

// newProcessValues collects the run process's types, defaults, and load settings.
func newProcessValues(
	ctx context.Context, run cwlcore.Process, scope *cwlcore.RequirementScope, decls []portDecl,
	resolver OutputResolver,
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
		resolver:       resolver,
	}

	for index := range decls {
		decl := &decls[index]
		pending.types[decl.Name] = decl.Type
		pending.loadListing[decl.Name] = decl.LoadListing
		pending.loadContents[decl.Name] = decl.LoadContents
	}

	for index := range decls {
		decl := &decls[index]
		pending.record(ctx, decl.Name, decl.DefaultNode, pending.runBase, pending.runDefaults)
	}

	return pending
}

// newPendingValues extends [newProcessValues] with step-level defaults and load requests.
func newPendingValues(
	ctx context.Context, sc cwlcore.StepContainer, step *plannedStep, decls []portDecl,
	resolver OutputResolver,
) *pendingValues {
	pending := newProcessValues(ctx, step.run, step.scope, decls, resolver)
	pending.stepBase = documentDir(sc)
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

// listingFor returns the effective loadListing for an input, applying the precedence chain.
func (p *pendingValues) listingFor(name string) cwlcore.LoadListingEnum {
	return cmp.Or(p.loadListing[name], p.listingDefault)
}

// record materializes a declaration's default and stores it under name.
func (p *pendingValues) record(
	ctx context.Context, name string, def salad.Node, base string, into map[string]deferredValue,
) {
	if def == nil || salad.IsNull(def) {
		return
	}

	into[name] = p.materialize(ctx, def, base, name)
}

// stepDefault returns the step's own default for name, or nil.
func (p *pendingValues) stepDefault(name string) (any, error) {
	return p.stepDefaults[name].get()
}

// fillProcessDefaults fills in the run process's defaults for null or missing parameters.
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

// materialize converts a default node into a Go value using the job-order loader.
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

// load applies loadContents/loadListing reads to a resolved input value.
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

		return loadFileContents(typed, p.resolver)
	case *cwlcore.Directory:
		return loadDirectoryListing(typed, p.listingFor(name), p.resolver)
	default:
		return value, nil
	}
}

// readsListing reports whether the loadListing mode requires reading.
func readsListing(mode cwlcore.LoadListingEnum) bool {
	return mode != "" && mode != cwlcore.LoadListingNone
}

// loadDirectoryListing reads a Directory's listing from disk at the requested depth.
// Skips if listing is already set, mode is no_listing, or path is empty.
func loadDirectoryListing(dir *cwlcore.Directory, mode cwlcore.LoadListingEnum, resolver OutputResolver) (any, error) {
	if !readsListing(mode) || dir.Listing != nil || dir.Path == "" {
		return dir, nil
	}

	var fsys WriteFS

	if resolver != nil {
		resolved, _, resolveErr := resolver.ResolveOutputFS(dir.Path)
		if resolveErr != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrLoadListing, dir.Path, resolveErr)
		}

		if wfs, ok := resolved.(WriteFS); ok {
			fsys = wfs
		} else {
			fsys = NewLocalDirFS(dir.Path)
		}
	} else {
		fsys = NewLocalDirFS(dir.Path)
	}

	listed, err := outCollectDirectory(dir.Path, mode, fsys, dir.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrLoadListing, dir.Path, err)
	}

	loaded := *dir
	loaded.Listing = listed.Listing

	return &loaded, nil
}

// loadFileContents reads a File's contents from disk. Returns a copy to avoid mutating shared values.
func loadFileContents(file *cwlcore.File, resolver OutputResolver) (any, error) {
	if file.Contents.IsSet() || file.Path == "" {
		return file, nil
	}

	var (
		stats outFileStats
		err   error
	)

	if resolver != nil {
		var (
			fsys fs.FS
			name string
		)

		fsys, name, err = resolver.ResolveOutputFS(file.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrLoadContents, file.Path, err)
		}

		stats, err = outDigestFS(fsys, name)
	} else {
		stats, err = outDigest(file.Path)
	}

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

// documentDir returns p's document directory, defaulting to ".".
func documentDir(p cwlcore.Process) string {
	return joProcessDir(p, ".")
}

// applyValueFrom evaluates valueFrom expressions on a copy of the input object.
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
