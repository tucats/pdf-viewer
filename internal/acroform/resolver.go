package acroform

import "github.com/tucats/pdf-viewer/internal/syntax"

// Resolver is the small slice of *internal/parser.Document's API this
// package needs - the same three-method shape internal/annotation,
// internal/fonts, internal/image, and internal/function each already
// declare their own copy of (see internal/image.Resolver's doc comment
// for the full "accept interfaces, return structs" rationale: every
// package in this module that needs to follow an indirect reference or
// decode a stream declares its own identically shaped interface rather
// than importing one another's, so that internal/acroform does not
// create a dependency on internal/model, internal/annotation, or
// internal/parser just to describe what it needs from one). Go's
// structural typing means *internal/model.Document already satisfies
// this without any changes on its side, and a caller already holding a
// value typed as internal/annotation.Resolver or internal/fonts.
// Resolver can pass it here directly.
//
// DecodeStream is not used by this package's own dictionary-walking code
// directly, but is required so that a Resolver value this package
// receives can be passed straight through to internal/fonts.Load (see
// appearance.go), which does need it to decode an embedded font
// program's stream.
type Resolver interface {
	Resolve(num int) (syntax.Object, error)
	ResolveDictionary(dict syntax.Dictionary) (syntax.Dictionary, error)
	DecodeStream(s syntax.Stream) ([]byte, error)
}

// resolveIfRef returns obj unchanged unless it is itself a
// syntax.Reference, in which case it resolves that reference through r -
// the same small pattern every other package in this module that walks
// PDF dictionaries and arrays already defines its own private copy of.
func resolveIfRef(r Resolver, obj syntax.Object) (syntax.Object, error) {
	ref, ok := obj.(syntax.Reference)
	if !ok {
		return obj, nil
	}
	return r.Resolve(ref.Number)
}

// dictEntry resolves dict[key] (following one level of indirect
// reference, if present) and reports whether the result is a
// syntax.Dictionary.
func dictEntry(r Resolver, dict syntax.Dictionary, key syntax.Name) (syntax.Dictionary, bool) {
	obj, ok := dict[key]
	if !ok {
		return nil, false
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return nil, false
	}
	d, ok := resolved.(syntax.Dictionary)
	return d, ok
}

// numberValue extracts a float64 from a syntax.Integer or syntax.Real -
// the same small helper every package in this module that walks PDF
// dictionaries defines its own copy of.
func numberValue(obj syntax.Object) (float64, bool) {
	switch v := obj.(type) {
	case syntax.Integer:
		return float64(v), true
	case syntax.Real:
		return float64(v), true
	default:
		return 0, false
	}
}

// floatArrayEntry resolves dict[key] (following a top-level reference,
// then each element's own reference) into a []float64, reporting
// ok=false for a missing key or any element that is not a number - used
// for /Rect and /MK's /BC and /BG color arrays.
func floatArrayEntry(r Resolver, dict syntax.Dictionary, key syntax.Name) ([]float64, bool) {
	obj, ok := dict[key]
	if !ok {
		return nil, false
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return nil, false
	}
	arr, ok := resolved.(syntax.Array)
	if !ok {
		return nil, false
	}
	out := make([]float64, len(arr))
	for i, e := range arr {
		re, err := resolveIfRef(r, e)
		if err != nil {
			return nil, false
		}
		v, ok := numberValue(re)
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}
