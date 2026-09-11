package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// requirementItems dumps a requirement list in document order.
func requirementItems(reqs []cwlcore.ProcessRequirement) []any {
	out := make([]any, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, requirementObject(req))
	}

	return out
}

// hintItems dumps a hint list in document order.
func hintItems(hints []cwlcore.Hint) []any {
	out := make([]any, 0, len(hints))
	for _, hint := range hints {
		out = append(out, hintObject(hint))
	}

	return out
}

// hintObject dumps one hint.
func hintObject(hint cwlcore.Hint) *cwlcli.Object {
	req, ok := hint.(cwlcore.ProcessRequirement)
	if ok {
		return requirementObject(req)
	}

	o := cwlcli.NewObject().Set("class", hint.Class())

	raw, ok := hint.(*cwlcore.RawHint)
	if !ok {
		return o
	}

	o.SetString("classIRI", raw.ClassIRI)

	return addNode(o, raw.Node)
}

// requirementObject dumps one requirement's class and fields.
func requirementObject(req cwlcore.ProcessRequirement) *cwlcli.Object {
	o := cwlcli.NewObject().Set("class", req.Class())

	addEnvironmentFields(o, req)
	addBudgetFields(o, req)
	addLanguageFields(o, req)

	return o
}

// addEnvironmentFields dumps container, software, workdir and env requirements.
func addEnvironmentFields(o *cwlcli.Object, req cwlcore.ProcessRequirement) {
	switch r := req.(type) {
	case *cwlcore.DockerRequirement:
		o.SetString("dockerPull", r.DockerPull)
		o.SetString("dockerLoad", r.DockerLoad)
		o.SetString("dockerFile", r.DockerFile)
		o.SetString("dockerImport", r.DockerImport)
		o.SetString("dockerImageId", r.DockerImageID)
		o.SetString("dockerOutputDirectory", r.DockerOutputDirectory)
	case *cwlcore.SoftwareRequirement:
		o.SetSlice("packages", softwarePackageItems(r.Packages))
	case *cwlcore.EnvVarRequirement:
		o.SetSlice("envDef", environmentDefItems(r.EnvDef))
	case *cwlcore.InitialWorkDirRequirement:
		o.Set("listing", listingValue(r.Listing))
	default:
		// Another group's requirement, or a marker with no fields.
	}
}

// addBudgetFields dumps resource and constraint requirements.
func addBudgetFields(o *cwlcli.Object, req cwlcore.ProcessRequirement) {
	switch r := req.(type) {
	case *cwlcore.ResourceRequirement:
		addResourceFields(o, r)
	case *cwlcore.ToolTimeLimit:
		o.Set("timelimit", r.Timelimit.String())
	case *cwlcore.NetworkAccess:
		o.Set("networkAccess", r.NetworkAccess.String())
	case *cwlcore.WorkReuse:
		o.Set("enableReuse", r.EnableReuse.String())
	case *cwlcore.InplaceUpdateRequirement:
		o.Set("inplaceUpdate", r.InplaceUpdate)
	case *cwlcore.LoadListingRequirement:
		o.SetString("loadListing", string(r.LoadListing))
	default:
		// Another group's requirement, or a marker with no fields.
	}
}

// resourceBound is one named bound of a ResourceRequirement.
type resourceBound struct {
	// Value is the bound as decoded: a number, an expression, or unset.
	Value cwlcore.ResourceValue
	// Key is the bound's name in the document.
	Key string
}

// addResourceFields dumps a ResourceRequirement's eight bounds.
func addResourceFields(o *cwlcli.Object, r *cwlcore.ResourceRequirement) {
	bounds := []resourceBound{
		{Key: "coresMin", Value: r.CoresMin},
		{Key: "coresMax", Value: r.CoresMax},
		{Key: "ramMin", Value: r.RAMMin},
		{Key: "ramMax", Value: r.RAMMax},
		{Key: "tmpdirMin", Value: r.TmpdirMin},
		{Key: "tmpdirMax", Value: r.TmpdirMax},
		{Key: "outdirMin", Value: r.OutdirMin},
		{Key: "outdirMax", Value: r.OutdirMax},
	}

	for _, bound := range bounds {
		if !bound.Value.IsSet() {
			continue
		}

		o.Set(bound.Key, bound.Value.String())
	}
}

// addLanguageFields dumps language-extension and raw requirements.
func addLanguageFields(o *cwlcli.Object, req cwlcore.ProcessRequirement) {
	switch r := req.(type) {
	case *cwlcore.InlineJavascriptRequirement:
		o.SetSlice("expressionLib", stringItems(r.ExpressionLib))
	case *cwlcore.SchemaDefRequirement:
		o.SetSlice("types", nodeItems(r.Types))
	case *cwlcore.RawRequirement:
		o.SetString("classIRI", r.ClassIRI)
		addNode(o, r.Node)
	default:
		// Another group's requirement, or a marker with no fields.
	}
}

// softwarePackageItems dumps a SoftwareRequirement's packages.
func softwarePackageItems(packages []cwlcore.SoftwarePackage) []any {
	out := make([]any, 0, len(packages))

	for _, pkg := range packages {
		o := cwlcli.NewObject()
		o.SetString("package", pkg.Package)
		o.SetSlice("version", stringItems(pkg.Version))
		o.SetSlice("specs", stringItems(pkg.Specs))
		out = append(out, o)
	}

	return out
}

// environmentDefItems dumps an EnvVarRequirement's variables.
func environmentDefItems(defs []cwlcore.EnvironmentDef) []any {
	out := make([]any, 0, len(defs))

	for _, def := range defs {
		out = append(out, cwlcli.NewObject().
			Set("envName", def.EnvName).
			Set("envValue", string(def.EnvValue)))
	}

	return out
}

// nodeItems materializes a slice of salad nodes as plain values.
func nodeItems(nodes []salad.Node) []any {
	out := make([]any, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, cwlcli.Plain(salad.ToAny(node)))
	}

	return out
}

// listingValue dumps an InitialWorkDirRequirement's listing.
func listingValue(listing cwlcore.InitialWorkDirListing) any {
	expr := listing.Expression()
	if expr != "" {
		return string(expr)
	}

	entries := listing.Entries()
	out := make([]any, 0, len(entries))

	for _, entry := range entries {
		out = append(out, listingEntry(entry))
	}

	return out
}

// listingEntry dumps one listing entry.
func listingEntry(entry cwlcore.InitialWorkDirEntry) any {
	dirent := entry.Dirent()
	if dirent != nil {
		o := cwlcli.NewObject()
		o.SetString("entryname", string(dirent.Entryname))
		o.SetString("entry", string(dirent.Entry))

		if dirent.Writable {
			o.Set("writable", true)
		}

		return o
	}

	file := entry.File()
	if file != nil {
		return cwlcli.NewObject().Set("class", file.Class()).SetString("location", file.Location)
	}

	directory := entry.Directory()
	if directory != nil {
		return cwlcli.NewObject().Set("class", directory.Class()).SetString("location", directory.Location)
	}

	return entry.String()
}

// addNode materializes a salad node as a plain value on the object.
func addNode(o *cwlcli.Object, node salad.Node) *cwlcli.Object {
	if node == nil {
		return o
	}

	return o.Set("node", cwlcli.Plain(salad.ToAny(node)))
}
