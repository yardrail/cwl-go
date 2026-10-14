package cwlcore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Resolving workflow step run references to their target processes.

// linkLocalRuns resolves run references against processes in the same document.
func linkLocalRuns(procs []Process) {
	index := newProcessIndex(procs)
	linked := make(map[Process]bool, len(procs))

	for _, process := range procs {
		index.link(process, linked)
	}
}

// processIndex maps process identifiers (full and fragment-only) to processes.
type processIndex struct {
	byID       map[string]Process
	byFragment map[string]Process
}

// newProcessIndex indexes procs and their embedded sub-processes.
func newProcessIndex(procs []Process) *processIndex {
	index := &processIndex{
		byID:       make(map[string]Process, len(procs)),
		byFragment: make(map[string]Process, len(procs)),
	}

	indexed := make(map[Process]bool, len(procs))
	for _, process := range procs {
		index.add(process, indexed)
	}

	return index
}

// add indexes one process and every process embedded under it.
func (idx *processIndex) add(p Process, indexed map[Process]bool) {
	if p == nil || indexed[p] {
		return
	}

	indexed[p] = true
	idx.record(p)

	sc, ok := p.(StepContainer)
	if !ok {
		return
	}

	for i := range sc.WorkflowSteps() {
		idx.add(sc.WorkflowSteps()[i].Run.Process, indexed)
	}
}

// record indexes a process under both its full ID and fragment.
func (idx *processIndex) record(p Process) {
	id := p.Base().ID
	if id == "" {
		return
	}

	if _, taken := idx.byID[id]; !taken {
		idx.byID[id] = p
	}

	fragment := idFragment(id)
	if _, taken := idx.byFragment[fragment]; !taken {
		idx.byFragment[fragment] = p
	}
}

// find returns the process a reference names, or nil.
func (idx *processIndex) find(ref string) Process {
	if p, ok := idx.byID[ref]; ok {
		return p
	}

	return idx.byFragment[idFragment(ref)]
}

// link resolves p's run references against the index, descending recursively.
func (idx *processIndex) link(p Process, linked map[Process]bool) {
	sc, ok := p.(StepContainer)
	if !ok || linked[p] {
		return
	}

	linked[p] = true

	steps := sc.WorkflowSteps()
	for i := range steps {
		run := &steps[i].Run
		if run.Process == nil && run.Ref != "" {
			run.Process = idx.find(run.Ref)
		}

		idx.link(run.Process, linked)
	}
}

// checkRunCycles reports a workflow that runs itself, directly or transitively.
func checkRunCycles(p Process) error {
	return walkRunGraph(p, make(map[Process]bool), make(map[Process]bool))
}

// walkRunGraph does a DFS cycle check on the run graph.
func walkRunGraph(p Process, onPath, done map[Process]bool) error {
	if onPath[p] {
		return salad.Errorf(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"%s runs itself, directly or through another workflow",
			describeProcess(p),
		)
	}

	sc, ok := p.(StepContainer)
	if !ok || done[p] {
		return nil
	}

	onPath[p] = true

	steps := sc.WorkflowSteps()
	for i := range steps {
		err := walkRunStep(&steps[i], onPath, done)
		if err != nil {
			return err
		}
	}

	delete(onPath, p)

	done[p] = true

	return nil
}

// walkRunStep descends into one step's run target for cycle checking.
func walkRunStep(step *WorkflowStep, onPath, done map[Process]bool) error {
	err := walkRunGraph(step.Run.Process, onPath, done)
	if err == nil {
		return nil
	}

	return stepError(step, err)
}

// describeProcess names a process for error messages, omitting blank node IDs.
func describeProcess(p Process) string {
	id := p.Base().ID
	if id == "" || strings.HasPrefix(id, blankNodePrefix) {
		return "the workflow"
	}

	return fmt.Sprintf("the workflow %q", id)
}

// stepError wraps an error with the step and run reference that caused it.
func stepError(step *WorkflowStep, err error) error {
	msg := fmt.Sprintf("the step %q cannot run %q, because", step.ID, step.Run.Ref)

	if nested, ok := errors.AsType[*salad.Error](err); ok {
		return salad.Group(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			msg,
			nested,
		)
	}

	return fmt.Errorf("%s %w", msg, err)
}

// runTarget is a run reference split into document URI and fragment.
type runTarget struct {
	uri      string
	fragment string
}

// runTargetOf resolves a run reference against its document's base URI.
func runTargetOf(base, ref string) (runTarget, error) {
	document := documentPart(ref)
	if document == "" {
		document = documentPart(base)
	}

	uri, err := documentFetcher().Normalize(documentPart(base), document)
	if err != nil {
		return runTarget{
				uri:      "",
				fragment: "",
			}, salad.Errorf(
				salad.SourceLine{
					File:  base,
					Start: salad.Position{Line: 0, Column: 0, Offset: 0},
					End:   salad.Position{Line: 0, Column: 0, Offset: 0},
				},
				"the reference cannot be resolved against %s: %s",
				base,
				err,
			)
	}

	return runTarget{uri: uri, fragment: fragmentPart(ref)}, nil
}

// namesUndeclaredObject reports whether ref names an object in the same document
// that local linking already failed to find.
func namesUndeclaredObject(base, ref string) bool {
	if fragmentPart(ref) == "" {
		return false
	}

	document := documentPart(ref)

	return document == "" || document == documentPart(base)
}

// externalRuns follows run references to other documents, caching loaded results.
type externalRuns struct {
	cache  map[string]Process
	linked map[Process]bool
	cfg    *loadConfig
}

// resolveExternalRuns follows all unresolved run references and checks for cycles.
func resolveExternalRuns(
	ctx context.Context,
	p Process,
	base, fragment string,
	cfg *loadConfig,
) error {
	runs := &externalRuns{
		cache:  map[string]Process{cacheKey(base, fragment): p},
		linked: make(map[Process]bool),
		cfg:    cfg,
	}

	err := runs.link(ctx, p, base)
	if err != nil {
		return err
	}

	return checkRunCycles(p)
}

// link resolves every step of one workflow.
func (e *externalRuns) link(ctx context.Context, p Process, base string) error {
	sc, ok := p.(StepContainer)
	if !ok || e.linked[p] {
		return nil
	}

	e.linked[p] = true

	steps := sc.WorkflowSteps()
	for i := range steps {
		err := e.linkStep(ctx, &steps[i], base)
		if err != nil {
			return err
		}
	}

	return nil
}

// linkStep resolves one step's run reference.
func (e *externalRuns) linkStep(ctx context.Context, step *WorkflowStep, base string) error {
	if step.Run.Process != nil {
		// Already linked — descend with same base.
		return e.link(ctx, step.Run.Process, base)
	}

	if step.Run.Ref == "" {
		return nil
	}

	if namesUndeclaredObject(base, step.Run.Ref) {
		return stepError(
			step,
			salad.Errorf(
				salad.SourceLine{
					File:  base,
					Start: salad.Position{Line: 0, Column: 0, Offset: 0},
					End:   salad.Position{Line: 0, Column: 0, Offset: 0},
				},
				"the document declares no object with that identifier",
			),
		)
	}

	process, err := e.loadTarget(ctx, base, step.Run.Ref)
	if err != nil {
		return stepError(step, err)
	}

	step.Run.Process = process

	return nil
}

// loadTarget resolves a reference and loads what it names.
func (e *externalRuns) loadTarget(ctx context.Context, base, ref string) (Process, error) {
	target, err := runTargetOf(base, ref)
	if err != nil {
		return nil, err
	}

	return e.load(ctx, target)
}

// load fetches, validates, decodes, and recursively links a target document.
func (e *externalRuns) load(ctx context.Context, target runTarget) (Process, error) {
	key := cacheKey(target.uri, target.fragment)
	if cached, ok := e.cache[key]; ok {
		return cached, nil
	}

	doc, err := loadFileDocument(ctx, target.uri, e.cfg)
	if err != nil {
		return nil, err
	}

	process, err := decodeTarget(doc, target.fragment)
	if err != nil {
		return nil, err
	}

	// Cache before descending to break self-referencing cycles.
	e.cache[key] = process

	err = e.link(ctx, process, doc.BaseURI)
	if err != nil {
		return nil, err
	}

	return process, nil
}

// cacheKey identifies a loaded object by document URI and fragment.
func cacheKey(uri, fragment string) string {
	return uri + "#" + fragment
}

// decodeTarget decodes the fragment's object, or the document's entry point if no fragment.
func decodeTarget(doc *salad.Document, fragment string) (Process, error) {
	if fragment == "" {
		return Decode(doc)
	}

	return decodeFragment(doc, fragment)
}

// decodeTargetWithSchema is decodeTarget with schema awareness for extension classes.
func decodeTargetWithSchema(doc *salad.Document, fragment string, loaded *salad.LoadedSchema) (Process, error) {
	var opts []decoderOption
	if loaded != nil {
		opts = append(opts, withLoadedSchema(loaded))
	}

	if fragment == "" {
		nodes, isGraph := graphNodes(doc.Root)

		entry := doc.Root
		if isGraph {
			main, err := selectMain(nodes, doc.BaseURI)
			if err != nil {
				return nil, err
			}

			entry = main
		}

		return decodeLinked(nodes, entry, opts...)
	}

	return decodeFragment(doc, fragment, opts...)
}
