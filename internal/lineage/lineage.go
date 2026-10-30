package lineage

import "github.com/jorben/tacet/internal/kernel"

// Deps is what the results of one stage depend on.
type Deps struct {
	Facets   []kernel.Facet
	Upstream []kernel.StageKind
}

// Table is the dependency table of module design 5.4. Decisions and the
// input version are tracked separately by jobs.
var Table = map[kernel.StageKind]Deps{
	kernel.StageExtractDetect:  {Facets: []kernel.Facet{kernel.FacetRuntime, kernel.FacetDetect}},
	kernel.StageSupplementScan: {Upstream: []kernel.StageKind{kernel.StageExtractDetect}},
	kernel.StageResolve:        {Facets: []kernel.Facet{kernel.FacetDispose}, Upstream: []kernel.StageKind{kernel.StageExtractDetect, kernel.StageSupplementScan}},
	kernel.StageExport:         {Facets: []kernel.Facet{kernel.FacetRender, kernel.FacetRuntime}, Upstream: []kernel.StageKind{kernel.StageResolve}},
	kernel.StageValidate:       {Facets: []kernel.Facet{kernel.FacetDetect, kernel.FacetRuntime}, Upstream: []kernel.StageKind{kernel.StageExport}},
	kernel.StagePublish:        {Facets: []kernel.Facet{kernel.FacetDelivery}, Upstream: []kernel.StageKind{kernel.StageValidate}},
}
