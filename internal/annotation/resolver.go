package annotation

import "github.com/tucats/pdf-viewer/internal/syntax"

// Resolver is the small slice of *internal/parser.Document's API this
// package needs - the same three-method shape internal/image,
// internal/fonts, and internal/function each already declare their own
// copy of (see internal/image.Resolver's doc comment for the full
// "accept interfaces, return structs" rationale). *internal/model.
// Document already satisfies this without any changes on its side, so
// the root package can pass it directly.
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
