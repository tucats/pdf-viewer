package parser

import (
	"fmt"

	"github.com/tucats/pdf-viewer/internal/filter"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Document.Resolve (turning an object number into
// its parsed value) and the linear-scan recovery path used when the
// cross-reference table's recorded offset for an object turns out to be
// wrong. See the package doc comment in parser.go for the rationale
// behind attempting recovery at all.

// Resolve returns the value of the indirect object numbered num,
// resolving it from wherever the cross-reference table (built by Open)
// says it lives.
//
// If num does not appear in the cross-reference table at all, or is
// recorded there as free (deleted), Resolve returns syntax.Null{} and a
// nil error - this matches the PDF specification's own rule that a
// reference to a nonexistent object behaves exactly like a reference to
// the null object, and lets callers (internal/model, in particular)
// treat "this optional dictionary entry was never provided" and "this
// entry points at a now-deleted object" identically, without needing to
// special-case either.
//
// If the offset the cross-reference table records for num turns out not
// to actually contain object num's definition (or resolving it fails for
// any other reason - a truncated stream, for example), Resolve makes one
// attempt to recover by scanning the entire file for "N G obj" markers
// (see recoverByScanning) and retries. If recovery also fails, or has
// already been attempted for this Document, the original error is
// returned. This linear-scan recovery only applies to objects with their
// own byte offset; an object packed inside an object stream (see
// objstream.go) has no "N G obj" marker of its own for such a scan to
// find in the first place, so a failure resolving one of those is
// returned directly.
func (d *Document) Resolve(num int) (syntax.Object, error) {
	if obj, ok := d.cache[num]; ok {
		return obj, nil
	}
	if d.resolving[num] {
		return nil, pdferror.Malformedf("cyclic indirect reference while resolving object %d", num)
	}

	entry, ok := d.xref[num]
	if !ok || entry.Free {
		return syntax.Null{}, nil
	}

	d.resolving[num] = true
	var obj syntax.Object
	var err error
	if entry.Compressed {
		// An object packed inside an object stream is never separately
		// encrypted: the object stream itself was already decrypted (as
		// an ordinary stream, via this same Resolve method, keyed by the
		// object stream's *own* number/generation) before
		// resolveCompressed ever parses the plaintext bytes packed
		// inside it - see internal/crypt's package doc comment and
		// objstream.go's loadObjectStream, which reaches this stream's
		// bytes via an ordinary d.Resolve call of its own.
		obj, err = d.resolveCompressed(entry)
	} else {
		obj, err = d.readObjectAt(num, entry.Offset)
		if err == nil {
			obj, err = d.decryptObject(num, entry.Generation, obj)
		}
	}
	delete(d.resolving, num)

	if err != nil {
		if entry.Compressed {
			return nil, err
		}
		obj, err = d.retryAfterRecovery(num, err)
		if err != nil {
			return nil, err
		}
	}

	d.cache[num] = obj
	return obj, nil
}

// decryptObject returns obj unchanged if this Document is not encrypted
// (d.crypt == nil - the common case), or the result of decrypting every
// string and stream found anywhere inside it (via internal/crypt's
// Handler.DecryptObject) otherwise. num and gen identify the indirect
// object obj was just parsed from, which the Standard Security
// Handler's per-object key derivation needs - see internal/crypt's
// ObjectKey.
func (d *Document) decryptObject(num, gen int, obj syntax.Object) (syntax.Object, error) {
	if d.crypt == nil {
		return obj, nil
	}
	return d.crypt.DecryptObject(num, gen, obj)
}

// retryAfterRecovery is called after an initial Resolve attempt fails.
// It runs the one-time whole-file recovery scan (recoverByScanning,
// which is a no-op on any call after the first) and, if that scan found
// a usable offset for num, retries reading the object from there. It
// returns firstErr - the original failure - if recovery does not help,
// so the error a caller ultimately sees always describes what actually
// went wrong rather than a generic "recovery failed" message.
func (d *Document) retryAfterRecovery(num int, firstErr error) (syntax.Object, error) {
	if d.recovered {
		return nil, firstErr
	}
	if scanErr := d.recoverByScanning(); scanErr != nil {
		return nil, firstErr
	}

	entry, ok := d.xref[num]
	if !ok || entry.Free {
		return nil, firstErr
	}

	d.resolving[num] = true
	obj, err := d.readObjectAt(num, entry.Offset)
	if err == nil {
		obj, err = d.decryptObject(num, entry.Generation, obj)
	}
	delete(d.resolving, num)
	if err != nil {
		return nil, firstErr
	}
	return obj, nil
}

// readObjectAt parses the indirect object definition expected at
// offset: "N G obj <value> endobj". It verifies that the object number
// actually found there matches num, which is what lets Resolve detect a
// cross-reference table pointing at the wrong place (as opposed to a
// pointer that happens to land on well-formed PDF syntax for some other
// reason) and trigger recovery.
func (d *Document) readObjectAt(num int, offset int64) (syntax.Object, error) {
	r, err := d.src.SectionFrom(offset)
	if err != nil {
		return nil, err
	}
	lex := syntax.NewLexer(r)

	gotNum, val, err := parseIndirectObject(lex)
	if err != nil {
		return nil, fmt.Errorf("object %d: %w", num, err)
	}
	if gotNum != num {
		return nil, pdferror.Malformedf("object %d: cross-reference table points at offset %d, which begins object %d instead", num, offset, gotNum)
	}
	return val, nil
}

// parseIndirectObject reads one complete indirect object definition -
// "N G obj <value> endobj" - from lex, having consumed nothing yet, and
// returns the object number found and its value. It is shared by
// readObjectAt above (which already knows which object number it
// expects, at a location the cross-reference table names) and
// loadXrefSection in parser.go (which does not: a cross-reference
// stream's own object number is only discovered by reading it, since
// nothing has pointed at it yet by the time it is read).
func parseIndirectObject(lex *syntax.Lexer) (num int, val syntax.Object, err error) {
	numTok, err := lex.Next()
	if err != nil {
		return 0, nil, err
	}
	// The generation number token is read (so the Lexer's position moves
	// past it) but not currently cross-checked against the
	// cross-reference entry's own recorded generation: this project
	// does not yet support multiple live generations of the same object
	// number within one open Document (see the Document.xref field's
	// doc comment), so the generation number in the file is, for now,
	// only ever informative.
	if _, err := lex.Next(); err != nil {
		return 0, nil, err
	}
	objTok, err := lex.Next()
	if err != nil {
		return 0, nil, err
	}

	if numTok.Kind != syntax.KindNumber || objTok.Kind != syntax.KindKeyword || objTok.Text != "obj" {
		return 0, nil, pdferror.Malformedf("no \"N G obj\" header found")
	}
	gotNum, ok := parseNonNegativeInt(numTok.Text)
	if !ok {
		return 0, nil, pdferror.Malformedf("object number %q is not a valid non-negative integer", numTok.Text)
	}

	val, err = syntax.ParseValue(lex, 0)
	if err != nil {
		return 0, nil, err
	}

	endTok, err := lex.Next()
	if err != nil {
		return 0, nil, err
	}
	if endTok.Kind != syntax.KindKeyword || endTok.Text != "endobj" {
		return 0, nil, pdferror.Malformedf("object %d: missing \"endobj\" keyword", gotNum)
	}

	return gotNum, val, nil
}

// resolveIfReference returns obj unchanged unless it is itself a
// syntax.Reference, in which case it resolves that reference through
// Resolve. PDF permits many dictionary entries to be either a direct
// value or an indirect reference to one interchangeably; callers that
// need a concrete value rather than "possibly another layer of
// indirection" go through this helper rather than duplicating the type
// assertion.
func (d *Document) resolveIfReference(obj syntax.Object) (syntax.Object, error) {
	ref, ok := obj.(syntax.Reference)
	if !ok {
		return obj, nil
	}
	return d.Resolve(ref.Number)
}

// ResolveDictionary returns a copy of dict with every top-level value
// that is a syntax.Reference resolved to what it actually points at -
// PDF permits essentially any dictionary entry to be given either
// directly or as an indirect reference to the same kind of value, and
// callers needing a concrete value (rather than "possibly another layer
// of indirection") use this rather than resolving each entry themselves.
// Only top-level entries are resolved, not values nested inside an
// array or a nested dictionary - see DecodeStream's doc comment, which
// documents this same limitation for the /Filter-decoding use it was
// originally written for.
//
// internal/image (Phase 3) also uses this: an image XObject's own
// dictionary entries (/Width, /Height, /ColorSpace, /SMask, and so on)
// are, in practice, essentially always direct values, but PDF does not
// require that, and internal/image has no cross-reference table of its
// own to resolve one with - see internal/image's package doc comment.
func (d *Document) ResolveDictionary(dict syntax.Dictionary) (syntax.Dictionary, error) {
	out := make(syntax.Dictionary, len(dict))
	for k, v := range dict {
		rv, err := d.resolveIfReference(v)
		if err != nil {
			return nil, fmt.Errorf("resolving /%s: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

// DecodeStream returns s's fully-decoded bytes: it resolves s.Dict via
// ResolveDictionary (its /Filter and /DecodeParms entries are, in the
// vast majority of real files, direct values already - but PDF
// technically permits them to be indirect references too) and hands the
// result to internal/filter.
//
// The Document is passed along as the filter package's StreamResolver,
// which one filter needs: a JBIG2Decode stream's /DecodeParms may name a
// /JBIG2Globals stream holding the symbol dictionary the image's own
// segments refer to, and reaching that second stream needs this
// Document's cross-reference table.
func (d *Document) DecodeStream(s syntax.Stream) ([]byte, error) {
	dict, err := d.ResolveDictionary(s.Dict)
	if err != nil {
		return nil, err
	}
	return filter.DecodeWith(dict, s.Raw, d)
}

// DecodeReferencedStream implements internal/filter.StreamResolver: it
// resolves obj - a reference to, or directly, a stream object - and
// returns that stream's decoded bytes.
//
// It deliberately decodes through filter.Decode rather than
// DecodeStream, so the stream it returns cannot itself pull in a third
// stream. Nothing legitimate needs that (a /JBIG2Globals stream is
// Flate-compressed or raw, never JBIG2-coded itself), and refusing to
// follow the chain is what keeps a hostile file whose globals stream
// names itself from recursing without end.
func (d *Document) DecodeReferencedStream(obj syntax.Object) ([]byte, error) {
	resolved, err := d.resolveIfReference(obj)
	if err != nil {
		return nil, err
	}
	s, ok := resolved.(syntax.Stream)
	if !ok {
		return nil, pdferror.Malformedf("expected a stream, found %T", resolved)
	}
	dict, err := d.ResolveDictionary(s.Dict)
	if err != nil {
		return nil, err
	}
	return filter.Decode(dict, s.Raw)
}

// maxRecoveryScanSize bounds how large a file recoverByScanning is
// willing to linearly scan. Recovery is already the exceptional, rare
// path (only reached when the cross-reference table itself is broken),
// so this is a generous limit rather than a tight one - but an
// unbounded scan over an arbitrarily large hostile file would still
// violate the "bounded work" requirement from the repository README's
// "Dependency and safety policy" section, so a limit exists regardless.
const maxRecoveryScanSize = 512 << 20 // 512 MiB

// recoverByScanning rebuilds d.xref from scratch by scanning the entire
// file for "N G obj" markers, recording each one's byte offset (via the
// Lexer's Pos method) as the new offset for object number N. Because the
// scan proceeds from the start of the file to the end, and later
// revisions of an object are always written later in the file than
// earlier ones (true both for a single, non-incrementally-updated
// document and for one with incremental updates - see the package doc
// comment), a later "N G obj" marker for the same N naturally overwrites
// an earlier one, which reconstructs the same "most recent revision
// wins" semantics loadXref maintains via the cross-reference table under
// normal (non-recovery) circumstances.
//
// This method is idempotent: it does nothing on any call after the
// first (see the recovered field), so a file with several broken
// objects only pays for one full-file scan no matter how many of
// Resolve's calls end up needing recovery.
func (d *Document) recoverByScanning() error {
	if d.recovered {
		return nil
	}
	d.recovered = true

	if d.src.Size() > maxRecoveryScanSize {
		return pdferror.Malformedf("file exceeds %d bytes; cross-reference recovery scan was not attempted", maxRecoveryScanSize)
	}

	r, err := d.src.SectionFrom(0)
	if err != nil {
		return err
	}
	lex := syntax.NewLexer(r)

	// A sliding window of the last three (token, byte-offset-it-started-
	// at) pairs is all that is needed to recognize the pattern "<int>
	// <int> obj" and recover the offset the first of those two numbers
	// started at - without building any real parse of the file, which
	// is exactly the point of this scan: find object boundaries cheaply,
	// trusting only that this one three-token pattern repeats throughout
	// the file. window[0] is the oldest of the three, window[2] the
	// newest (the token just read).
	type positioned struct {
		tok syntax.Token
		pos int64
	}
	var window [3]positioned
	seen := 0

	found := 0
	for {
		pos := lex.Pos()
		tok, err := lex.Next()
		if err != nil {
			return err
		}
		if tok.Kind == syntax.KindEOF {
			break
		}

		window[0], window[1], window[2] = window[1], window[2], positioned{tok: tok, pos: pos}
		seen++

		if seen >= 3 &&
			window[0].tok.Kind == syntax.KindNumber &&
			window[1].tok.Kind == syntax.KindNumber &&
			window[2].tok.Kind == syntax.KindKeyword && window[2].tok.Text == "obj" {
			if num, ok := parseNonNegativeInt(window[0].tok.Text); ok {
				d.xref[num] = xrefEntry{Offset: window[0].pos, Generation: 0}
				found++
			}
		}
	}

	if found == 0 {
		return pdferror.Malformedf("recovery scan found no \"N G obj\" markers in the file")
	}
	return nil
}
