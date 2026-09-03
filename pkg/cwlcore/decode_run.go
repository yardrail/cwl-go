package cwlcore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Resolving a workflow step's run reference to the process it names.
//
// A step either embeds its process inline, names a sibling declared in the same
// document, or names another document entirely. Only the first arrives decoded,
// and a reference still unfollowed by the time a Workflow reaches a scheduler is
// a reference the scheduler would have to do I/O for. So they are followed here,
// in two passes that split along exactly the line between what needs I/O and
// what does not:
//
//   - Decode links every reference that names a process the same document
//     declares. That needs nothing but the document, so it happens even for a
//     caller who drove pkg/salad itself, and it is what makes a packed $graph
//     executable.
//   - Load and LoadFile follow what is left, fetching and decoding the documents
//     named through the same memoized schema every other entry point uses. A
//     reference that still resolves to nothing is fatal there, because there is
//     nowhere else left to look.
//
// StepRun.Ref stays populated either way. What a step pointed at is worth
// keeping for diagnostics, and once Process is filled in it is the only record
// of it.

// linkLocalRuns points every run reference that names one of procs at the
// process it names, descending through what it links so that a chain of
// references inside one document is followed to the end.
//
// A reference that names nothing here is left alone rather than reported: it may
// name another document, which only Load and LoadFile can follow.
func linkLocalRuns(procs []Process) {
	index := newProcessIndex(procs)
	linked := make(map[Process]bool, len(procs))

	for _, process := range procs {
		index.link(process, linked)
	}
}

// processIndex maps the identifiers one document declares to the processes they
// name, both in full and by fragment alone.
//
// Two spellings are kept because a reference and the identifier it names do not
// always arrive spelled alike: an unresolved document writes both as "#tool",
// while a resolved one writes both in full — but a document assembled from the
// two, or resolved by some other means, may mix them.
type processIndex struct {
	byID       map[string]Process
	byFragment map[string]Process
}

// newProcessIndex indexes procs and everything embedded under them.
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

// record files one process under both spellings of its identifier. The first
// process to claim a spelling keeps it, so an outer declaration is never
// displaced by something embedded under it.
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

// find returns the process a reference names, or nil when this document
// declares none.
func (idx *processIndex) find(ref string) Process {
	if p, ok := idx.byID[ref]; ok {
		return p
	}

	return idx.byFragment[idFragment(ref)]
}

// link resolves p's run references against the index, then descends into
// whatever each step turned out to run.
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

// checkRunCycles reports a workflow that runs itself, directly or through
// another workflow.
//
// The specification makes a self-invoking workflow a fatal error, and it is
// worth finding here rather than at scheduling time: once the references are
// linked, a cycle is a loop of pointers that any consumer walking the graph
// would follow forever.
func checkRunCycles(p Process) error {
	return walkRunGraph(p, make(map[Process]bool), make(map[Process]bool))
}

// walkRunGraph descends the run graph depth first, reporting the back edge that
// closes a cycle.
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

// walkRunStep descends into one step, naming it when the cycle is below it.
func walkRunStep(step *WorkflowStep, onPath, done map[Process]bool) error {
	err := walkRunGraph(step.Run.Process, onPath, done)
	if err == nil {
		return nil
	}

	return stepError(step, err)
}

// describeProcess names a process for an error message, saying nothing about
// the identifier when it is the blank node one decoding invented, which would
// mean nothing to a reader of the document.
func describeProcess(p Process) string {
	id := p.Base().ID
	if id == "" || strings.HasPrefix(id, blankNodePrefix) {
		return "the workflow"
	}

	return fmt.Sprintf("the workflow %q", id)
}

// stepError blames a step for an error raised while following its run
// reference, keeping the underlying error tree intact when there is one.
//
// The fallback wraps rather than renders. A step's run: target is a document in
// its own right and may fail for a reason a caller has to be able to recognize
// rather than merely print — a cwlVersion this implementation has no schema for
// is the case that matters, since the cwl-runner contract answers it with a
// different exit status. Flattening the cause into the message would have made
// that indistinguishable from a malformed document.
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

// runTarget is a run reference split into the document to load and the object to
// select inside it.
type runTarget struct {
	uri      string
	fragment string
}

// runTargetOf resolves a run reference against the base URI of the document that
// wrote it, which is what makes a relative reference mean the same thing however
// the process running it was invoked.
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

// namesUndeclaredObject reports whether a run reference names an object inside
// the document that wrote it — which, since local linking has already run and
// found nothing, means an object that document does not declare.
//
// A reference to the same document carrying no fragment is a different thing
// entirely: it names that document's own entry point, which is a workflow
// invoking itself. That is reported as the cycle it is rather than as a missing
// identifier, so it is deliberately not caught here.
func namesUndeclaredObject(base, ref string) bool {
	if fragmentPart(ref) == "" {
		return false
	}

	document := documentPart(ref)

	return document == "" || document == documentPart(base)
}

// externalRuns follows the run references that name other documents, loading
// each document once however many steps point at it.
//
// The loader and the flattened schema behind it are the memoized ones every
// entry point shares, so a workflow whose ten steps run the same tool pays for
// one fetch and no extra flatten.
type externalRuns struct {
	cache  map[string]Process
	linked map[Process]bool
	cfg    *loadConfig
}

// resolveExternalRuns follows every run reference p leaves unresolved, reports
// the first that names nothing loadable, and then rejects a run cycle.
//
// p is entered into the document cache under the reference it was loaded by, so
// that a workflow reached again through its own file is recognized as the same
// workflow rather than loaded a second time.
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

// link resolves every step of one workflow, with base the URI of the document
// that workflow was written in.
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
		// Already linked: inline, or against the document that wrote it,
		// so it shares that document's base.
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

// load fetches, validates and decodes the document a target names, following its
// own run references before returning it.
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

	// Cached before descending, so that a document reachable from itself
	// terminates here and is reported by the cycle check rather than by
	// running out of stack.
	e.cache[key] = process

	err = e.link(ctx, process, doc.BaseURI)
	if err != nil {
		return nil, err
	}

	return process, nil
}

// cacheKey identifies one loaded object: the document it came from, and the
// fragment that selected it inside that document.
func cacheKey(uri, fragment string) string {
	return uri + "#" + fragment
}

// decodeTarget decodes the object a reference's fragment names, or the
// document's entry point when it names none.
func decodeTarget(doc *salad.Document, fragment string) (Process, error) {
	if fragment == "" {
		return Decode(doc)
	}

	return decodeFragment(doc, fragment)
}

// decodeTargetWithSchema is decodeTarget with the loaded schema threaded
// through to the decoder, so that extension process classes that extend
// Workflow can be recognized and decoded as ExtensionWorkflow.
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
