package plan

// Look is how a rasterised output paints its regions: the resolution and the redaction style frozen in the
// snapshot's render facet (requirements FR-29). It never changes which pixels are covered, and the
// validator is given the same look, so it checks the regions hold exactly that texture.
type Look struct {
	DPI   int
	Style string
}

// AddTo puts the look into the parameters of a format.write or format.validate call; a zero field is left
// out and the plugin uses its default.
func (l Look) AddTo(params map[string]any) map[string]any {
	if l.DPI != 0 {
		params["dpi"] = l.DPI
	}
	if l.Style != "" {
		params["style"] = l.Style
	}
	return params
}
