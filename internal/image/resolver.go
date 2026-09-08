package image

import "github.com/tucats/pdf-viewer/internal/syntax"

// Resolver is the small slice of *internal/parser.Document's API this
// package needs in order to finish decoding an image whose dictionary
// contains something this package cannot interpret entirely on its own:
// an indirect reference (Resolve), a stream dictionary with its own
// possibly-indirect entries (ResolveDictionary), or a stream that still
// needs its /Filter chain applied (DecodeStream) - a named /ColorSpace
// resource, an /Indexed color space's lookup table, an /ICCBased color
// space's stream, and a separate /SMask or /Mask image all need one or
// more of these.
//
// This interface is declared here, in the package that consumes it,
// rather than the caller (internal/content) importing
// internal/parser.Document directly and passing that concrete type -
// this is the standard Go idiom "accept interfaces, return structs":
// internal/content ends up needing to accept the same interface for its
// own "Do"/"BI" handling (since it calls into this package), and
// *parser.Document already implements this exact method set without any
// changes on its side, so nothing needs to be adapted for the two
// packages to agree on what a "resolver" is.
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
// This is the same small "maybe it's a reference, maybe it's a direct
// value" pattern internal/parser and internal/model each already define
// their own private copy of (see, for example, internal/model's
// resolveObject) - PDF allows a dictionary entry to be either
// interchangeably almost everywhere, and every package that walks
// dictionaries ends up needing this one-line check somewhere.
func resolveIfRef(r Resolver, obj syntax.Object) (syntax.Object, error) {
	ref, ok := obj.(syntax.Reference)
	if !ok {
		return obj, nil
	}
	return r.Resolve(ref.Number)
}
