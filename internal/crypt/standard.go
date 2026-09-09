package crypt

import (
	"crypto/md5"
	"encoding/binary"
)

// This file implements the "classic" Standard Security Handler key
// derivation used by revisions 2, 3, and 4 (ISO 32000-1 §7.6.3,
// Algorithms 2, 3, 4, and 5) - the RC4 and AES-128 (AESV2) case. The
// newer AES-256 (revision 5-6) algorithm lives in hash56.go instead,
// since it is a different (and unrelated) hash-based construction, not
// a variant of this one.

// padBytes is the fixed 32-byte "padding string" that ISO 32000-1
// §7.6.3.3 defines for this algorithm. Every password (including the
// empty one this package always uses - see the package doc comment) is
// padded out to exactly 32 bytes using a prefix of this string before
// being hashed; an empty password's padded form is simply this entire
// string. The specific bytes have no meaning of their own - they were
// simply chosen by the original algorithm's authors as an arbitrary,
// fixed 32-byte constant that every compliant reader and writer must
// agree on byte-for-byte, so this array cannot be changed or
// "simplified" without breaking compatibility with every real-world PDF
// that uses this security handler.
var padBytes = [32]byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41,
	0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// PadPassword returns password padded (or truncated) to exactly 32
// bytes per ISO 32000-1 §7.6.3.3, Algorithm 2 step (a): if password is
// 32 bytes or longer, only its first 32 bytes are used; otherwise it is
// followed by as many bytes of padBytes as needed to reach 32. Passing
// an empty password (this package's only supported case - see the
// package doc comment) returns padBytes unchanged.
func PadPassword(password []byte) []byte {
	out := make([]byte, 32)
	n := copy(out, password)
	copy(out[n:], padBytes[:])
	return out
}

// permissionBytes returns p (the /P dictionary entry, a signed 32-bit
// permissions bitmask) as its 4 little-endian bytes, exactly as
// Algorithm 2 step (c) and Algorithm 3 step (c) require: "Pass the
// value of the P entry ... low-order byte first". Go's binary package
// works with unsigned integers, so p is reinterpreted as uint32 (a
// bit-for-bit, not a value-preserving, conversion) before encoding -
// which is exactly the "treat these 32 bits as bytes" operation the
// algorithm calls for.
func permissionBytes(p int32) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(p))
	return buf[:]
}

// ComputeOwnerHash implements Algorithm 3 ("Computing the encryption
// dictionary's O (owner password) value"), ISO 32000-1 §7.6.3.4. It
// returns the 32-byte /O entry that belongs in a Standard Security
// Handler's /Encrypt dictionary for the given owner and user passwords.
//
// This package only ever calls it (from its own tests, and from
// tools/genfixtures - see the package doc comment's "Why some of this
// package's functions are exported" section) with both passwords empty,
// since decrypting an empty-user-password document never needs to
// authenticate an owner password - but the algorithm itself does not
// care, so it is implemented in full.
func ComputeOwnerHash(ownerPassword, userPassword []byte, r, keyLenBytes int) []byte {
	// Step (a)-(b): hash the padded owner password (or the user
	// password, per the spec, when no owner password is set - callers
	// needing that fallback pass userPassword as both arguments) with
	// MD5, then - for revision 3 and up - re-hash the result's first
	// keyLenBytes bytes 50 more times. This repeated re-hashing exists
	// only to make brute-forcing the password more expensive; it has no
	// other effect on the algorithm's output shape.
	sum := md5.Sum(PadPassword(ownerPassword))
	key := sum[:]
	if r >= 3 {
		for i := 0; i < 50; i++ {
			sum = md5.Sum(key[:keyLenBytes])
			key = sum[:]
		}
	}
	okey := key[:keyLenBytes]

	// Step (d): RC4-encrypt the padded *user* password with okey.
	result := mustRC4(okey, PadPassword(userPassword))

	// Step (e): for revision 3 and up, repeat the encryption 19 more
	// times, each round XORing every byte of okey with the round number
	// before using it as that round's RC4 key - the same "make this
	// expensive to invert" idea as step (b)'s repeated hashing.
	if r >= 3 {
		roundKey := make([]byte, len(okey))
		for i := 1; i <= 19; i++ {
			for j := range okey {
				roundKey[j] = okey[j] ^ byte(i)
			}
			result = mustRC4(roundKey, result)
		}
	}

	// The result is always exactly 32 bytes (the padded password's own
	// length), regardless of keyLenBytes.
	return result
}

// ComputeFileKey implements Algorithm 2 ("Computing an encryption key"),
// ISO 32000-1 §7.6.3.3, for revisions 2-4. It derives the document's
// file encryption key from a candidate user password (already
// revision-appropriately encoded - see encodePassword - and possibly
// empty, either because none was supplied or because the document's own
// user password really is empty) plus values already present in the
// /Encrypt dictionary and trailer: the owner-password hash o (the /O
// entry, see ComputeOwnerHash), the permissions bitmask p (the /P
// entry), and the document identifier id0 (the trailer /ID array's
// first element). encryptMetadata is the /EncryptMetadata entry
// (defaulting to true when absent); when it is false and r is 4 or
// higher, step (f) mixes in four extra 0xFF bytes, per the algorithm.
// The caller (New, handler.go) does not yet know whether password is
// correct - that is what validateUserPassword, using this function's
// result, determines next.
func ComputeFileKey(password, o []byte, p int32, id0 []byte, r, keyLenBytes int, encryptMetadata bool) []byte {
	h := md5.New()
	h.Write(PadPassword(password))  // step (a)
	h.Write(o)                      // step (b): O must be exactly 32 bytes
	h.Write(permissionBytes(p))     // step (c)
	h.Write(id0)                    // step (d)
	if r >= 4 && !encryptMetadata { // step (f)
		h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	sum := h.Sum(nil) // step (e), first pass
	key := sum[:keyLenBytes]

	// Step (g): revision 3 and up re-hashes the key 50 more times, each
	// round hashing only the current keyLenBytes-byte key (not the full
	// 16-byte MD5 output from the previous round) - the same
	// "deliberately expensive" repetition ComputeOwnerHash's step (b)
	// uses.
	if r >= 3 {
		for i := 0; i < 50; i++ {
			roundSum := md5.Sum(key)
			key = roundSum[:keyLenBytes]
		}
	}
	return key
}

// ComputeUserHash implements Algorithms 4 and 5 ("Computing the
// encryption dictionary's U (user password) value"), ISO 32000-1
// §7.6.3.4, returning the 32-byte /U entry that a Standard Security
// Handler using fileKey (see ComputeFileKey) would write for the given
// revision. internal/crypt's own password-validation logic (see
// handler.go) runs this same computation on a candidate file key and
// compares the result against the /U entry actually found in the
// document: if they match, the (here, always empty) password used to
// derive fileKey was correct.
func ComputeUserHash(fileKey []byte, r int, id0 []byte) []byte {
	if r == 2 {
		// Algorithm 4: U is simply the 32-byte padding string, encrypted
		// once with the file key.
		return mustRC4(fileKey, padBytes[:])
	}

	// Algorithm 5 (revision 3 and up):
	// (a)-(b) MD5-hash the padding string concatenated with the first
	// element of the document /ID array.
	h := md5.New()
	h.Write(padBytes[:])
	h.Write(id0)
	digest := h.Sum(nil)

	// (c) RC4-encrypt that 16-byte digest with the file key.
	result := mustRC4(fileKey, digest)

	// (d) 19 more encryption rounds, each XORing every byte of the file
	// key with the round number first - structurally identical to
	// ComputeOwnerHash's step (e) above.
	roundKey := make([]byte, len(fileKey))
	for i := 1; i <= 19; i++ {
		for j := range fileKey {
			roundKey[j] = fileKey[j] ^ byte(i)
		}
		result = mustRC4(roundKey, result)
	}

	// (e) The final /U value is 32 bytes: the 16-byte result from (d)
	// followed by 16 bytes of arbitrary padding - the specification
	// explicitly does not constrain what these trailing bytes are, since
	// a reader validating a password only ever compares the first 16
	// bytes (see handler.go's validateUserPassword). This package always
	// writes them as zero for a fully deterministic, reproducible
	// output - useful for tools/genfixtures, which needs byte-identical
	// fixtures across regenerations (see its own doc comment).
	out := make([]byte, 32)
	copy(out, result)
	return out
}
