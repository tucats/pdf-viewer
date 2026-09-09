// Package crypt implements the PDF Standard Security Handler (ISO
// 32000-1 §7.6.3, and its ISO 32000-2 §7.6.4 successor for the newer
// AES-256 revisions), which is what a PDF trailer's /Encrypt dictionary
// names when a document has been "protected" - most commonly to restrict
// printing/copying/editing (permissions) while still letting any reader
// open the file without prompting for a password.
//
// # If you are new to PDF encryption
//
// A "protected" PDF almost always has *two* passwords, both optional:
//
//   - The owner password controls the document's permissions (can it be
//     printed? copied? edited?) - a compliant reader is trusted to
//     respect these, though nothing stops a reader (this one included)
//     from ignoring them, since this package does not attempt to enforce
//     them at all; only decryption is implemented.
//   - The user password, if set, must be supplied just to *open* the
//     file at all.
//
// The single most common real-world case - bank statements, invoices,
// print-to-PDF output - sets an owner password (so permissions are
// recorded) but leaves the user password empty, precisely so that any
// PDF reader can open the file freely (Phase 7a). The other real case -
// a document that genuinely will not open without a password - is also
// supported (Phase 7b): the root package's WithPassword option passes a
// caller-supplied password all the way down to this package's New,
// which validates it exactly the way any PDF reader's "enter password"
// dialog would (see New's doc comment). Either way, this package only
// ever tries the supplied (or empty) password as the document's *user*
// password, not its owner password - see New's doc comment for that
// scope decision. A password that does not validate is reported as
// ErrWrongPassword, letting internal/parser distinguish "this document
// needs a password" from "the password given was wrong" in the error
// message it produces (both still classify as the same ErrEncrypted
// sentinel a caller checks with errors.Is - see docs/PLAN2.md's Phase 7
// and the root package's error taxonomy).
//
// # The three things a security handler does
//
//  1. Key derivation: turn a password (empty, unless the caller supplied
//     one via WithPassword) plus a few values already sitting in the
//     file's /Encrypt dictionary and trailer (the owner-password hash
//     /O, the permissions bitmask /P, the document /ID) into a single
//     "file encryption key" - see standard.go for the classic (revision
//     2-4) algorithm and hash56.go for the newer (revision 5-6,
//     AES-256) one.
//  2. Per-object keys: revisions 2-4 mix the file key with each
//     individual object's number and generation before use (Algorithm
//     1, key.go), so that no two objects in the file are encrypted with
//     literally the same key. Revision 5-6 (AES-256) skips this step
//     entirely and uses the file key directly - see key.go's doc
//     comment for why.
//  3. The actual cipher: RC4 or AES-CBC, both from the standard
//     library's crypto/rc4, crypto/aes, and crypto/cipher packages - see
//     cipher.go. No third-party dependency or CGO is used anywhere in
//     this package, matching this project's "Dependency and safety
//     policy".
//
// Handler (handler.go) ties all three steps together into the small API
// internal/parser actually calls: New to open an encrypted document
// (which also performs the password-validation step, Algorithm 6 or its
// AES-256 equivalent, so a document requiring a real password is
// rejected with a clear error rather than silently decrypting into
// garbage), then DecryptString and DecryptStream per object as
// internal/parser resolves each one.
//
// # Why some of this package's functions are exported
//
// A handful of functions most callers would expect to be private
// implementation detail (ComputeOwnerHash, ComputeUserHash, and so on)
// are exported anyway. The reason is testing: this package's own tests,
// and tools/genfixtures (which builds this project's test PDF fixtures
// without importing anything outside the standard library plus this
// module's own internal packages), both need to *build* a
// spec-correct encrypted PDF from scratch in order to verify this
// package can read one back correctly - there being no other way to
// obtain a real encrypted fixture with a known-correct answer without
// either shipping a third-party PDF ourselves (this project avoids
// depending on external fixtures where a hand-authored one will do; see
// tools/genfixtures's own doc comment) or reimplementing the same math
// a second time in two places, which would risk the two copies quietly
// drifting apart. Being exported from this internal package has no
// effect on this module's actual public API (the pdfviewer package):
// Go's "internal/" directory convention already prevents anything
// outside this module from importing internal/crypt at all, exported or
// not.
package crypt
