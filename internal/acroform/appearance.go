package acroform

import "github.com/tucats/pdf-viewer/internal/syntax"

// GenerateAppearance is this package's single entry point: given
// widgetDict (a widget annotation dictionary that internal/annotation.
// ResolveOne has already reported has no usable existing appearance -
// see the root package's annotations.go, the only intended caller) and
// acroForm (the document catalog's /AcroForm dictionary, or nil for a
// document with none - see internal/model.Document.AcroForm), returns a
// ready-to-paint Form XObject stream together with the /Rect it should
// be mapped onto - exactly the two pieces internal/annotation.
// FromStream needs to finish placing it on the page, reusing that
// package's own BBox-to-Rect mapping algorithm rather than
// reimplementing it here.
//
// ok is false whenever there is nothing to generate: an unusable or
// missing /Rect, a field type this package does not handle (/FT /Sig,
// or no /FT at all), or - for the field types it does handle - any of
// the more specific reasons textfield.go/checkbox.go's own doc comments
// give (no value, a password field, a push button with no state to
// show, an unresolvable /DA font, ...). Every one of these is this
// package's ordinary "nothing usable here" outcome, not an error - a
// caller simply leaves that widget unpainted, exactly as if this
// package did not exist, matching internal/annotation.Resolve's own
// "annotations are optional" tolerance.
func GenerateAppearance(r Resolver, widgetDict syntax.Dictionary, acroForm syntax.Dictionary) (syntax.Stream, []float64, bool) {
	rect, ok := floatArrayEntry(r, widgetDict, "Rect")
	if !ok || len(rect) != 4 {
		return syntax.Stream{}, nil, false
	}
	width, height := rectSize(rect)
	if width <= 0 || height <= 0 {
		return syntax.Stream{}, nil, false
	}

	f := ResolveWithAcroForm(r, widgetDict, acroForm)

	var (
		content   []byte
		resources syntax.Dictionary
	)
	switch f.FT {
	case "Tx", "Ch":
		content, resources, ok = buildTextAppearance(r, f, width, height)
	case "Btn":
		content, resources, ok = buildCheckboxAppearance(r, widgetDict, f, width, height)
	default:
		// /FT /Sig (no generic "current value" to paint) or an empty/
		// unrecognized /FT (most likely a widget this package cannot
		// even identify as a form field's own) - see this package's doc
		// comment on scope.
		ok = false
	}
	if !ok {
		return syntax.Stream{}, nil, false
	}

	dict := syntax.Dictionary{
		"Type":      syntax.Name("XObject"),
		"Subtype":   syntax.Name("Form"),
		"BBox":      syntax.Array{syntax.Real(0), syntax.Real(0), syntax.Real(width), syntax.Real(height)},
		"Resources": resources,
	}
	return syntax.Stream{Dict: dict, Raw: content}, rect, true
}

// rectSize returns rect's ([llx lly urx ury]) width and height,
// normalizing for the possibility that PDF does not require llx<urx or
// lly<ury (see internal/annotation's identical tolerance).
func rectSize(rect []float64) (width, height float64) {
	width = rect[2] - rect[0]
	if width < 0 {
		width = -width
	}
	height = rect[3] - rect[1]
	if height < 0 {
		height = -height
	}
	return width, height
}
