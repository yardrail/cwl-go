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

// The representations this tool can dump.
const (
	// StageParsed is the raw YAML parse tree before resolution.
	StageParsed Stage = "parsed"
	// StageResolved is the salad Document after loading and schema validation.
	StageResolved Stage = "resolved"
	// StageTyped is the typed model decoded from the resolved tree.
	StageTyped Stage = "typed"
	// StageGraph is every top-level process in the document.
	StageGraph Stage = "graph"
	// StageScope is the resolved requirements and hints in effect.
	StageScope Stage = "scope"
)

// ErrStage is the error [Stage.Set] reports for an unknown stage.
var ErrStage = errors.New("unknown stage")

// stageOrder lists the stages in processing order.
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

// Inspect builds the stage's view of the document at ref.
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

// inspectProcess loads the entry-point process and dumps it through project.
func inspectProcess(ref string, project func(cwlcore.Process) *cwlcli.Object) (any, error) {
	process, err := cwlcore.LoadFile(context.Background(), ref)
	if err != nil {
		return nil, err
	}

	return project(process), nil
}
