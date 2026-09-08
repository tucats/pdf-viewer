package fonts

import "github.com/tucats/pdf-viewer/internal/syntax"

// Resolver is the small slice of *internal/parser.Document's API this
// package needs to finish reading a font whose dictionary contains
// something it cannot interpret entirely on its own: an indirect
// reference (Resolve), a font or font-descriptor dictionary whose own
// entries may themselves be indirect references (ResolveDictionary), or
// an embedded font program stream that still needs its /Filter chain
// applied (DecodeStream) - an embedded TrueType program (/FontFile2) is
// almost always Flate-compressed in real files, for example.
//
// This is the exact same three-method shape as internal/image.Resolver
// (see that package's doc comment for the full "accept interfaces,
// return structs" rationale, which applies here unchanged) - declared as
// its own named type, rather than imported from internal/image, so this
// package does not need to depend on internal/image just to reuse an
// interface shape the two packages happen to need identically. Go's
// structural typing means *internal/parser.Document (and, through it,
// internal/model.Document - see that package's resolver.go) already
// satisfies this interface without any changes on their side, and a
// caller holding a value already typed as internal/image.Resolver can
// pass it here directly: an interface value's type only needs to have at
// least these three methods, not be named Resolver specifically.
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
	// internal/parser.Document.DecodeStream.
	DecodeStream(s syntax.Stream) ([]byte, error)
}

// resolveIfRef returns obj unchanged unless it is itself a
// syntax.Reference, in which case it resolves that reference through r.
// This is the same small pattern internal/parser, internal/model, and
// internal/image each already define their own private copy of - PDF
// allows a dictionary entry to be either interchangeably almost
// everywhere, and every package that walks dictionaries ends up needing
// this one-line check somewhere.
func resolveIfRef(r Resolver, obj syntax.Object) (syntax.Object, error) {
	ref, ok := obj.(syntax.Reference)
	if !ok {
		return obj, nil
	}
	return r.Resolve(ref.Number)
}
