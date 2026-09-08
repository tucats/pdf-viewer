package syntax

import "fmt"

// This file defines the Go types used to represent every kind of value
// that can appear in a PDF file's object syntax: booleans, numbers,
// strings, names, arrays, dictionaries, streams, indirect references,
// and null. Higher layers (internal/parser, internal/model, ...) work
// with these types rather than re-parsing bytes themselves.
//
// If you are new to Go: PDF's object model is a classic "one of several
// kinds of thing" (a sum type / discriminated union), which Go does not
// have built-in syntax for the way some languages do. The idiomatic Go
// way to model this is an interface with an unexported marker method
// (Object.isObject here) implemented only by the types that are allowed
// to be PDF objects. That "seals" the interface: code outside this
// package cannot accidentally (or intentionally) create a new type that
// satisfies Object, which keeps every switch over Object's possible
// concrete types exhaustive and safe to reason about. A type switch
// (`switch v := obj.(type) { case Name: ...; case Integer: ... }`) is
// the normal way to inspect an Object's actual kind.

// Object is implemented by every PDF object value type defined in this
// file: Null, Boolean, Integer, Real, String, Name, Array, Dictionary,
// Stream, and Reference.
type Object interface {
	// isObject is unexported so that only types in this package can
	// implement Object; see the package-level comment above.
	isObject()
}

// Null represents the PDF null object, written as the keyword "null".
// It is a distinct type (rather than, say, a nil Object) because PDF
// itself distinguishes "this key is present with value null" from "this
// key is absent" - the former still means something in some contexts,
// so collapsing the two would lose information a caller might need.
type Null struct{}

func (Null) isObject() {}

// Boolean represents a PDF boolean, written as the keyword "true" or
// "false".
type Boolean bool

func (Boolean) isObject() {}

// Integer represents a PDF integer number. The PDF specification does
// not fix a bit width for integers, but int64 comfortably covers every
// value that occurs in real PDF files (object numbers, byte offsets,
// array lengths, and so on).
type Integer int64

func (Integer) isObject() {}

// Real represents a PDF real (floating point) number, written with a
// decimal point, e.g. "3.14", "-.5", "4.".
type Real float64

func (Real) isObject() {}

// String represents a PDF string object, holding its already-decoded
// bytes. PDF has two surface syntaxes for strings - literal strings in
// parentheses, e.g. "(Hello)", and hexadecimal strings in angle
// brackets, e.g. "<48656C6C6F>" - but both denote the same kind of
// value (an arbitrary byte sequence, not necessarily valid text in any
// particular encoding) once escapes and hex digits have been decoded,
// so this package represents both as String and does not retain which
// surface syntax was used.
type String []byte

func (String) isObject() {}

// Name represents a PDF name object, written as a slash followed by
// characters, e.g. "/Type". The value stored here is the decoded name
// text with the leading slash removed and any "#xx" hex escapes
// resolved - so the name "/A#42" decodes to Name("AB").
type Name string

func (Name) isObject() {}

// Array represents a PDF array object, written in square brackets, e.g.
// "[1 2 3]". Elements may be any Object, including nested arrays,
// dictionaries, or references.
type Array []Object

func (Array) isObject() {}

// Dictionary represents a PDF dictionary object, written in double
// angle brackets, e.g. "<< /Type /Catalog /Pages 2 0 R >>". Per the PDF
// specification, a dictionary's keys are always names, and are unique
// within one dictionary (a repeated key overwrites the earlier value) -
// both of which map naturally onto a Go map keyed by Name.
//
// Dictionary order is not preserved, matching Go's normal (and PDF's
// own) treatment of dictionaries as unordered key/value sets; nothing in
// the PDF specification assigns meaning to the order keys were written
// in.
type Dictionary map[Name]Object

func (Dictionary) isObject() {}

// Get looks up key in the dictionary and reports whether it was
// present. This exists mainly so calling code reads naturally
// (`if v, ok := dict.Get("Type"); ok { ... }`) without needing to know
// Dictionary is a plain Go map under the hood.
func (d Dictionary) Get(key Name) (Object, bool) {
	v, ok := d[key]
	return v, ok
}

// Reference represents an indirect reference to another object, written
// as "N G R" where N is an object number and G is a generation number,
// e.g. "5 0 R". Resolving a Reference to the Object it actually points
// at is internal/parser's job, not this package's - this package only
// knows how to recognize and represent the reference syntax itself.
type Reference struct {
	Number     int
	Generation int
}

func (Reference) isObject() {}

// String implements fmt.Stringer so references print in the familiar
// "N G R" form in error messages and test failure output, rather than
// Go's default struct formatting.
func (r Reference) String() string {
	return fmt.Sprintf("%d %d R", r.Number, r.Generation)
}

// Stream represents a PDF stream object: a dictionary immediately
// followed by a run of raw bytes bracketed by the "stream" and
// "endstream" keywords, e.g. "<< /Length 5 >> stream\nHello\nendstream".
//
// Raw holds the stream's bytes exactly as they appear in the file,
// still encoded by whatever filters (if any) its dictionary's /Filter
// entry names - decoding those filters is a job for a higher layer
// (planned for internal/content in Phase 2, per the repository README's
// phased plan).
//
// Locating exactly where a stream's raw bytes end is normally done by
// reading /Length bytes after the "stream" keyword, but /Length is
// sometimes itself an indirect reference (e.g. "/Length 9 0 R") rather
// than a direct integer - and resolving an indirect reference is
// internal/parser's job, not this package's, since it requires
// consulting the cross-reference table. When this package's parser
// encounters a non-direct-integer /Length, it falls back to scanning
// forward for the literal "endstream" keyword instead; see ParseValue's
// doc comment for details.
type Stream struct {
	Dict Dictionary
	Raw  []byte
}

func (Stream) isObject() {}
