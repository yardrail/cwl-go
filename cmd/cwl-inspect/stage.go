package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Stage names one of the intermediate representations a CWL document passes
// through on its way to being executable. It implements [flag.Value].
type Stage string

// The representations this tool can dump. They are different debugging views,
// not different verbosities of one view: the parse tree is what you look at
// when the document will not load, the resolved tree when resolution or
// decoding is wrong, the typed model when execution is wrong, and the scope
// when a requirement is not reaching the tool that needs it.
const (
	// StageParsed is the salad Node tree exactly as the YAML parser
	// produced it, before any $import, identifier or vocabulary
	// resolution, with the source line of every node.
	StageParsed Stage = "parsed"
	// StageResolved is the salad Document the loader produced: the same
	// tree after $import and $include splicing, identifier resolution and
	// vocabulary expansion, and after schema validation, but before any
	// typed decode.
	StageResolved Stage = "resolved"
	// StageTyped is the typed model pkg/cwlcore decoded from that tree.
	StageTyped Stage = "typed"
	// StageGraph is every top-level process in the document rather than
	// only the one the entry-point rules select.
	StageGraph Stage = "graph"
	// StageScope is the requirements and hints in effect for the process,
	// and for each of a workflow's steps, with where each came from.
	StageScope Stage = "scope"
)

// ErrStage is the error [Stage.Set] reports for an unknown stage.
var ErrStage = errors.New("unknown stage")

// stageOrder lists the stages in the order a document passes through them,
// which is also the order the usage message presents them.
var stageOrder = []Stage{StageParsed, StageResolved, StageTyped, StageGraph, StageScope}

// String returns the stage's flag spelling, satisfying [flag.Value].
func (s *Stage) String() string {
	if s == nil || *s == "" {
		return string(StageTyped)
	}

	return string(*s)
}

// Set parses the stage's flag spelling, satisfying [flag.Value].
func (s *Stage) Set(v string) error {
	if !slices.Contains(stageOrder, Stage(v)) {
		return fmt.Errorf("%w %q: expected %s", ErrStage, v, Stages())
	}

	*s = Stage(v)

	return nil
}

// Stages lists the accepted stage spellings, for a usage message.
func Stages() string {
	names := make([]string, 0, len(stageOrder))
	for _, stage := range stageOrder {
		names = append(names, string(stage))
	}

	return strings.Join(names, "|")
}

// Inspect builds the stage's view of the document at ref, as a value the
// renderers can write in any format.
func (s *Stage) Inspect(ref string) (any, error) {
	switch Stage(s.String()) {
	case StageParsed:
		return inspectParsed(ref)
	case StageResolved:
		return inspectResolved(ref)
	case StageGraph:
		return inspectGraph(ref)
	case StageScope:
		return inspectProcess(ref, scopeObject)
	default:
		return inspectProcess(ref, processObject)
	}
}

// inspectParsed reads the document and dumps its parse tree.
//
// It is the only stage that does not go through pkg/cwlcore, and that is the
// point: a document that will not load is exactly when you need to see what
// the parser made of it.
func inspectParsed(ref string) (any, error) {
	src, url, err := cwlcli.Fetch(ref)
	if err != nil {
		return nil, err
	}

	root, err := salad.Parse(url, src)
	if err != nil {
		return nil, err
	}

	return nodeObject(root), nil
}

// inspectResolved dumps the resolved, validated document without decoding it.
func inspectResolved(ref string) (any, error) {
	doc, err := cwlcore.LoadFileDocument(context.Background(), ref)
	if err != nil {
		return nil, err
	}

	return documentObject(doc), nil
}

// inspectGraph dumps every top-level process in the document.
func inspectGraph(ref string) (any, error) {
	doc, err := cwlcore.LoadFileDocument(context.Background(), ref)
	if err != nil {
		return nil, err
	}

	processes, err := cwlcore.DecodeAll(doc)
	if err != nil {
		return nil, err
	}

	return graphObject(doc, processes), nil
}

// inspectProcess loads the document's entry-point process and dumps it through
// project, which is what separates the typed view from the scope view.
func inspectProcess(ref string, project func(cwlcore.Process) *cwlcli.Object) (any, error) {
	process, err := cwlcore.LoadFile(context.Background(), ref)
	if err != nil {
		return nil, err
	}

	return project(process), nil
}
