package parser

import (
	"errors"

	"github.com/tucats/pdf-viewer/internal/crypt"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Document.setupEncryption, called once from Open
// when the trailer declares an /Encrypt entry. It is a thin adapter
// between this package's syntax.Dictionary/Document world and
// internal/crypt's Handler (which knows nothing about cross-reference
// tables or object resolution, only about the Standard Security
// Handler's cryptography) - see internal/crypt's package doc comment
// for the actual key-derivation and decryption algorithms.

// setupEncryption resolves the trailer's /Encrypt entry and, if it
// names a Standard Security Handler that validates under an empty user
// password, records the resulting crypt.Handler in d.crypt so that
// every later Resolve call decrypts what it reads.
//
// Every failure path below - a malformed /Encrypt dictionary, an
// unsupported revision or security handler, or (via
// crypt.ErrWrongPassword) a document that genuinely requires a
// caller-supplied password this package cannot yet provide (see
// docs/PLAN2.md's Phase 7b) - is deliberately reported as an error
// wrapping pdferror.ErrEncrypted rather than as whatever more specific
// error (ErrMalformed, ErrUnsupported) actually caused it. This matches
// this package's pre-Phase-7 behavior of rejecting *any* /Encrypt
// dictionary uniformly: from a caller's perspective, "this document
// declares itself encrypted and this package could not fully open it"
// is one coherent situation (see the root package's errors.go, "Error
// taxonomy" section) worth a single, specific sentinel to check for,
// regardless of which particular detail of the encryption setup this
// package could not handle.
//
// This method must run *before* d.crypt is set (which it does - d.crypt
// starts nil and this is the only place that ever assigns it), so that
// resolving the /Encrypt dictionary itself, and the trailer's /ID
// array, never attempts to decrypt anything: both are always stored as
// plaintext even in an encrypted document, since they are exactly what
// a reader needs in order to work out *how* to decrypt everything else.
func (d *Document) setupEncryption() error {
	handler, err := d.buildEncryptionHandler()
	if err != nil {
		if errors.Is(err, crypt.ErrWrongPassword) {
			// The one situation Phase 7a explicitly leaves unhandled: a
			// document that really does require a caller-supplied
			// password to open at all, as opposed to the "opens freely,
			// permissions recorded via an owner password" case this
			// package now handles transparently.
			return pdferror.Encryptedf("document requires a non-empty password, which is not yet supported")
		}
		return pdferror.Encryptedf("could not set up the document's Standard Security Handler: %v", err)
	}
	d.crypt = handler
	return nil
}

// buildEncryptionHandler does the actual work setupEncryption wraps: it
// is split out only so that setupEncryption has one single place to
// apply the ErrEncrypted-wrapping policy documented above, rather than
// repeating it at every possible failure point below.
func (d *Document) buildEncryptionHandler() (*crypt.Handler, error) {
	encObj, err := d.resolveIfReference(d.Trailer["Encrypt"])
	if err != nil {
		return nil, err
	}
	dict, ok := encObj.(syntax.Dictionary)
	if !ok {
		return nil, pdferror.Malformedf("/Encrypt does not resolve to a dictionary (found %T)", encObj)
	}

	id0, err := d.trailerID0()
	if err != nil {
		return nil, err
	}

	return crypt.New(dict, id0)
}

// trailerID0 returns the first element of the trailer's /ID array (the
// permanent, original-creation-time file identifier ISO 32000-1
// §14.4 describes - not to be confused with any object's own object
// number), as raw bytes ready to feed into internal/crypt's key
// derivation. Per §7.6.3.3, this is a required input to that algorithm,
// but a small number of real-world encrypted files omit /ID anyway (a
// producer bug, not something this package can correct) - rather than
// failing every such file outright, an absent /ID is treated as an
// empty byte string, matching the tolerant, "still try to open it"
// posture the rest of this package takes toward malformed input (see
// the package doc comment's "Recovering from a corrupted cross-reference
// table" section for the same philosophy applied elsewhere).
func (d *Document) trailerID0() ([]byte, error) {
	idObj, ok := d.Trailer["ID"]
	if !ok {
		return nil, nil
	}
	idObj, err := d.resolveIfReference(idObj)
	if err != nil {
		return nil, err
	}
	idArray, ok := idObj.(syntax.Array)
	if !ok || len(idArray) == 0 {
		return nil, nil
	}
	first, err := d.resolveIfReference(idArray[0])
	if err != nil {
		return nil, err
	}
	idString, ok := first.(syntax.String)
	if !ok {
		return nil, nil
	}
	return []byte(idString), nil
}
