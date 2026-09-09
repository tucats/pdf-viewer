package crypt

import (
	"bytes"
	"errors"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file is internal/crypt's actual public entry point: New builds a
// Handler from a document's /Encrypt dictionary (already resolved to a
// concrete syntax.Dictionary - see internal/parser's setupEncryption,
// its only caller) and the document's ID, and a Handler's DecryptObject
// method is what internal/parser calls on every object it resolves from
// an encrypted document, decrypting every string and every stream's raw
// bytes found anywhere inside it.

// Method identifies which cipher (if any) actually protects a stream or
// string once a Handler has worked out which one applies - see New's
// /CF, /StmF, and /StrF handling below for how a V4/V5 document can
// specify different methods for streams versus strings, and even opt
// specific ones out of encryption entirely via MethodIdentity.
type Method int

const (
	// MethodIdentity means "not actually encrypted" - PDF permits a V4
	// or V5 document to name the built-in /Identity crypt filter for
	// /StmF or /StrF (or to omit them, which defaults to /Identity),
	// meaning streams or strings (respectively) pass through unchanged
	// even though the document as a whole declares an /Encrypt
	// dictionary.
	MethodIdentity Method = iota
	// MethodRC4 is the original RC4 stream cipher - the only method
	// revisions 2 and 3 support, and the "/V2" crypt filter method name
	// under revision 4.
	MethodRC4
	// MethodAESV2 is AES-128 in CBC mode - the "/AESV2" crypt filter
	// method name, available starting at revision 4.
	MethodAESV2
	// MethodAESV3 is AES-256 in CBC mode - the "/AESV3" crypt filter
	// method name, used exclusively by revisions 5 and 6 (and, for those
	// revisions, the only method: see New's handling of r >= 5, which
	// does not even look at /CF/StmF/StrF).
	MethodAESV3
)

// ErrWrongPassword is returned by New when the document's /U (or, for
// revision 5-6, /U and /UE) entries do not validate against the
// password New was given - whether that password was the empty string
// (no password supplied at all, or the document's user password really
// is empty and something else about it is wrong) or a real,
// caller-supplied one that simply does not match. internal/parser wraps
// this into a pdferror.Encryptedf error - with a message distinguishing
// "no password was supplied" from "the supplied password did not work"
// - so a caller sees the same ErrEncrypted sentinel as any other
// encrypted-and-unreadable document, distinguishable from a
// malformed-file error.
var ErrWrongPassword = errors.New("crypt: document requires a password to open, or the password supplied was incorrect")

// Handler decrypts the strings and streams of one already-opened,
// encrypted PDF document. It is built once, by New, when
// internal/parser.Document.Open finds a trailer /Encrypt entry, and then
// used for the lifetime of that Document.
type Handler struct {
	// fileKey is the document's file encryption key: 5-16 bytes for
	// revisions 2-4 (see ComputeFileKey), or exactly 32 bytes for
	// revisions 5-6 (see ComputeFileKeyR56).
	fileKey []byte
	r       int

	// stmMethod and strMethod say which cipher protects stream data and
	// string data respectively - almost always the same method in
	// practice, but PDF permits a document to encrypt one and not the
	// other (or use different ciphers for each) via /StmF and /StrF, so
	// this package tracks them independently rather than assuming they
	// match.
	stmMethod Method
	strMethod Method

	// encryptMetadata mirrors the /EncryptMetadata entry (default true):
	// when false, a stream whose /Type is /Metadata is stored
	// unencrypted even though the rest of the document is encrypted -
	// see DecryptObject's handling of this.
	encryptMetadata bool
}

// New builds a Handler for a document whose already-resolved /Encrypt
// dictionary is dict, using the trailer's /ID array's first element
// (id0) as required by the key-derivation algorithms in standard.go and
// hash56.go, and password as the user password to try - the empty
// string if the caller supplied none (see the root package's
// WithPassword option, and internal/parser's setupEncryption, this
// function's real caller). It returns ErrWrongPassword if password does
// not validate against the document's own /U (and, for revision 5-6,
// /UE) entries, whether because it is genuinely wrong or because none
// was supplied for a document that requires one.
//
// New only ever tries password as the *user* password (via Algorithm 6
// for revisions 2-4, or its revision 5-6 equivalent) - it does not
// attempt to also try password as an *owner* password (which would
// require unwrapping /O, ISO 32000-1 Algorithm 7, to recover the user
// password it was built from). This project's own scope decision (see
// docs/PLAN2.md's Phase 7) is that a document's user password is what a
// caller supplies to open a file - if a document's user password is
// empty (Phase 7a's case), New already finds that via password == "".
//
// dict must not itself have been decrypted (it never should be: the
// /Encrypt dictionary's own strings, such as /O and /U, are always
// stored in plaintext - decrypting them would be circular, since they
// are exactly what New uses to work out whether decryption is even
// possible in the first place). internal/parser's setupEncryption
// resolves the /Encrypt object before creating a Handler, guaranteeing
// this.
func New(dict syntax.Dictionary, id0 []byte, password string) (*Handler, error) {
	if filter, ok := dict["Filter"].(syntax.Name); ok && filter != "Standard" {
		return nil, pdferror.Unsupportedf("security handler %q (only the Standard security handler is supported)", filter)
	}

	v := intEntry(dict, "V", 0)
	r := intEntry(dict, "R", 0)
	if r < 2 || r > 6 {
		return nil, pdferror.Unsupportedf("Standard security handler revision %d", r)
	}

	o, ok := dict["O"].(syntax.String)
	if !ok {
		return nil, pdferror.Malformedf("/Encrypt dictionary missing a direct /O string")
	}
	u, ok := dict["U"].(syntax.String)
	if !ok {
		return nil, pdferror.Malformedf("/Encrypt dictionary missing a direct /U string")
	}
	encryptMetadata := boolEntry(dict, "EncryptMetadata", true)
	passwordBytes := encodePassword(password, r)

	if r >= 5 {
		return newHandlerR56(dict, r, []byte(u), passwordBytes, encryptMetadata)
	}
	return newHandlerR234(dict, v, r, []byte(o), []byte(u), id0, passwordBytes, encryptMetadata)
}

// newHandlerR56 builds a Handler for revision 5 or 6 (AES-256 - see
// hash56.go), the branch of New that unwraps /UE rather than recomputing
// a hash-based file key from scratch. password is already
// revision-appropriately encoded (see encodePassword), not a raw Go
// string.
func newHandlerR56(dict syntax.Dictionary, r int, u, password []byte, encryptMetadata bool) (*Handler, error) {
	if len(u) != 48 {
		return nil, pdferror.Malformedf("/Encrypt /U must be 48 bytes for revision %d, found %d", r, len(u))
	}
	ue, ok := dict["UE"].(syntax.String)
	if !ok || len(ue) != 32 {
		return nil, pdferror.Malformedf("/Encrypt dictionary missing a direct, 32-byte /UE string required for revision %d", r)
	}

	fileKey, err := ComputeFileKeyR56(password, r, u, []byte(ue))
	if err != nil {
		return nil, err
	}

	return &Handler{
		fileKey:         fileKey,
		r:               r,
		stmMethod:       MethodAESV3,
		strMethod:       MethodAESV3,
		encryptMetadata: encryptMetadata,
	}, nil
}

// newHandlerR234 builds a Handler for revisions 2-4 (RC4 and AES-128 -
// see standard.go and key.go). password is already
// revision-appropriately encoded (see encodePassword), not a raw Go
// string.
func newHandlerR234(dict syntax.Dictionary, v, r int, o, u, id0, password []byte, encryptMetadata bool) (*Handler, error) {
	if len(o) != 32 {
		return nil, pdferror.Malformedf("/Encrypt /O must be 32 bytes for revision %d, found %d", r, len(o))
	}

	keyLenBits := intEntry(dict, "Length", 40)
	if v == 1 {
		// Revision 2 (always paired with V=1) hard-codes a 40-bit key
		// regardless of what /Length says; V=1 predates /Length's
		// introduction in the specification, so a V=1 document is not
		// expected to even have one.
		keyLenBits = 40
	}
	keyLenBytes := keyLenBits / 8
	if keyLenBytes < 5 || keyLenBytes > 16 || keyLenBits%8 != 0 {
		return nil, pdferror.Malformedf("/Encrypt /Length %d is not a valid key length in bits", keyLenBits)
	}

	p := int32(intEntry(dict, "P", 0))
	fileKey := ComputeFileKey(password, o, p, id0, r, keyLenBytes, encryptMetadata)

	if !validateUserPassword(fileKey, r, id0, u) {
		return nil, ErrWrongPassword
	}

	stmMethod, strMethod := MethodRC4, MethodRC4
	if v == 4 {
		stmMethod, strMethod = resolveCryptFilters(dict)
	}

	return &Handler{
		fileKey:         fileKey,
		r:               r,
		stmMethod:       stmMethod,
		strMethod:       strMethod,
		encryptMetadata: encryptMetadata,
	}, nil
}

// validateUserPassword implements Algorithm 6 ("Authenticating the user
// password"), ISO 32000-1 §7.6.3.4: it reports whether fileKey - already
// derived from a candidate password via ComputeFileKey - is actually
// correct, by recomputing what /U *would* be for that key (see
// ComputeUserHash) and comparing it against the document's real u.
//
// Revision 2's U is a full, deterministic 32 bytes, so every byte must
// match. Revision 3 and up's U has 16 arbitrary padding bytes appended
// (see ComputeUserHash's own doc comment) that a validator is not
// supposed to compare, so only the first 16 bytes need to match there.
func validateUserPassword(fileKey []byte, r int, id0, u []byte) bool {
	computed := ComputeUserHash(fileKey, r, id0)
	if r == 2 {
		return bytes.Equal(computed, u)
	}
	return len(u) >= 16 && bytes.Equal(computed[:16], u[:16])
}

// resolveCryptFilters reads a revision-4 /Encrypt dictionary's /CF
// (crypt filters), /StmF (stream filter), and /StrF (string filter)
// entries and returns the Method each one names. Per ISO 32000-1
// §7.6.5, /StmF and /StrF each default to the built-in name /Identity
// (meaning "not encrypted") when absent - a document that sets /V 4 but
// no /StmF/StrF is, technically, choosing to encrypt nothing at all;
// unusual, but this function follows the specification's default rather
// than guessing the document meant /StdCF.
func resolveCryptFilters(dict syntax.Dictionary) (stm, str Method) {
	cf, _ := dict["CF"].(syntax.Dictionary)

	lookup := func(entry syntax.Object, ok bool) Method {
		if !ok {
			return MethodIdentity
		}
		name, ok := entry.(syntax.Name)
		if !ok || name == "Identity" {
			return MethodIdentity
		}
		filterDict, ok := cf[name].(syntax.Dictionary)
		if !ok {
			return MethodIdentity
		}
		switch filterDict["CFM"] {
		case syntax.Name("AESV2"):
			return MethodAESV2
		case syntax.Name("AESV3"):
			return MethodAESV3
		case syntax.Name("V2"):
			return MethodRC4
		default:
			return MethodIdentity
		}
	}

	stmEntry, stmOK := dict["StmF"]
	strEntry, strOK := dict["StrF"]
	return lookup(stmEntry, stmOK), lookup(strEntry, strOK)
}

// DecryptString decrypts one string object's already-parsed bytes, using
// whichever method (h.strMethod) the document's /Encrypt dictionary
// selected for strings, and the per-object key Algorithm 1 derives from
// num and gen (the encrypting object's own object and generation
// numbers - see ObjectKey's doc comment for why those matter for
// revisions 2-4, and why they are ignored entirely for revision 5-6).
func (h *Handler) DecryptString(num, gen int, data []byte) ([]byte, error) {
	return h.decrypt(h.strMethod, num, gen, data)
}

// DecryptStream decrypts one stream object's raw (still filter-encoded -
// see internal/parser.Document.DecodeStream, which runs after this, not
// before) bytes, using whichever method (h.stmMethod) the document's
// /Encrypt dictionary selected for streams.
func (h *Handler) DecryptStream(num, gen int, data []byte) ([]byte, error) {
	return h.decrypt(h.stmMethod, num, gen, data)
}

func (h *Handler) decrypt(method Method, num, gen int, data []byte) ([]byte, error) {
	switch method {
	case MethodIdentity:
		return data, nil
	case MethodRC4:
		return mustRC4(ObjectKey(h.fileKey, num, gen, false), data), nil
	case MethodAESV2:
		return aesDecryptCBC(ObjectKey(h.fileKey, num, gen, true), data)
	case MethodAESV3:
		// Revision 5-6 never mixes the object number/generation into the
		// key - see key.go's ObjectKey doc comment - so every object is
		// decrypted directly with the file key.
		return aesDecryptCBC(h.fileKey, data)
	default:
		return nil, pdferror.Malformedf("crypt: unknown method %d", method)
	}
}

// DecryptObject walks obj (the freshly-parsed value of indirect object
// num, generation gen) and returns a copy with every syntax.String
// decrypted via DecryptString and every syntax.Stream's raw bytes
// decrypted via DecryptStream, recursing into syntax.Dictionary and
// syntax.Array values along the way. Every other syntax.Object type
// (Integer, Real, Boolean, Null, Name, Reference) carries no encrypted
// content of its own and is returned unchanged - a Reference in
// particular is never followed here: if it is later resolved to another
// object via internal/parser.Document.Resolve, that object is decrypted
// separately, keyed by *its own* object number and generation, not
// num/gen.
//
// This is internal/parser's only real integration point with this
// package beyond New itself: internal/parser.Document.Resolve calls this
// on every object it reads from an encrypted document, exactly once,
// right after parsing it and before caching it - see that method's own
// comments.
func (h *Handler) DecryptObject(num, gen int, obj syntax.Object) (syntax.Object, error) {
	switch v := obj.(type) {
	case syntax.String:
		dec, err := h.DecryptString(num, gen, []byte(v))
		if err != nil {
			return nil, err
		}
		return syntax.String(dec), nil

	case syntax.Dictionary:
		out := make(syntax.Dictionary, len(v))
		for k, val := range v {
			dv, err := h.DecryptObject(num, gen, val)
			if err != nil {
				return nil, err
			}
			out[k] = dv
		}
		return out, nil

	case syntax.Array:
		out := make(syntax.Array, len(v))
		for i, val := range v {
			dv, err := h.DecryptObject(num, gen, val)
			if err != nil {
				return nil, err
			}
			out[i] = dv
		}
		return out, nil

	case syntax.Stream:
		decDict, err := h.DecryptObject(num, gen, v.Dict)
		if err != nil {
			return nil, err
		}
		dict, _ := decDict.(syntax.Dictionary)

		// ISO 32000-1 §7.6.1: when /EncryptMetadata is false, a
		// /Metadata stream is deliberately left unencrypted (so that
		// tools that only need to read metadata, such as search
		// indexers, need not implement decryption at all) even though
		// every other stream in the same document is encrypted
		// normally.
		if !h.encryptMetadata {
			if t, ok := dict["Type"].(syntax.Name); ok && t == "Metadata" {
				return syntax.Stream{Dict: dict, Raw: v.Raw}, nil
			}
		}

		raw, err := h.DecryptStream(num, gen, v.Raw)
		if err != nil {
			return nil, err
		}
		return syntax.Stream{Dict: dict, Raw: raw}, nil

	default:
		return obj, nil
	}
}

func intEntry(dict syntax.Dictionary, key syntax.Name, def int) int {
	if n, ok := dict[key].(syntax.Integer); ok {
		return int(n)
	}
	return def
}

func boolEntry(dict syntax.Dictionary, key syntax.Name, def bool) bool {
	if b, ok := dict[key].(syntax.Boolean); ok {
		return bool(b)
	}
	return def
}
