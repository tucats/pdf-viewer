package function

import "github.com/tucats/pdf-viewer/internal/syntax"

// Resolver is the small slice of *internal/parser.Document's API this
// package needs to read a function dictionary or stream whose entries
// may themselves be indirect references (a Domain/Range/Size/Encode/
// Decode/Bounds array element, a Type 3 function's Functions array
// entry, or - for a Type 0 function - the sample data stream itself).
//
// This is the exact same three-method shape as internal/image.Resolver
// and internal/fonts.Resolver (see either's doc comment for the full
// "accept interfaces, return structs" rationale, which applies here
// unchanged): declared as its own named type rather than imported from
// one of those packages, so this package does not depend on either just
// to reuse an interface shape they happen to need identically. Go's
// structural typing means any value already satisfying one of those
// interfaces (in particular, *internal/parser.Document, and therefore
// internal/model.Document and internal/image.Resolver values generally)
// already satisfies this one too, with no adapter needed.
type Resolver interface {
	// Resolve returns the value of indirect object number num - see
	// internal/parser.Document.Resolve's doc comment for the full
	// contract (including that a nonexistent object resolves to
	// syntax.Null with no error, matching a dangling reference).
	Resolve(num int) (syntax.Object, error)

	// ResolveDictionary returns a copy of dict with every top-level
	// syntax.Reference value resolved to what it actually points at -
	// see internal/parser.Document.ResolveDictionary.
	ResolveDictionary(dict syntax.Dictionary) (syntax.Dictionary, error)

	// DecodeStream returns s's bytes with every filter named in its
	// dictionary's /Filter entry applied, in order - see
	// internal/parser.Document.DecodeStream. A Type 0 function's sample
	// data is almost always FlateDecode-compressed in real files.
	DecodeStream(s syntax.Stream) ([]byte, error)
}

// resolveIfRef returns obj unchanged unless it is itself a
// syntax.Reference, in which case it resolves that reference through r.
// This is the same small pattern every other package in this module that
// walks PDF dictionaries and arrays already defines its own private copy
// of - see, for example, internal/image's identical helper.
func resolveIfRef(r Resolver, obj syntax.Object) (syntax.Object, error) {
	ref, ok := obj.(syntax.Reference)
	if !ok {
		return obj, nil
	}
	return r.Resolve(ref.Number)
}
