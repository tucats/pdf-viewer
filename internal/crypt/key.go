package crypt

import "crypto/md5"

// aesSalt is the fixed 4-byte string "sAlT" that ISO 32000-1 §7.6.2's
// Algorithm 1 mixes into the object key derivation only when the object
// is going to be encrypted with AES rather than RC4 - an arbitrary
// constant both a compliant reader and writer must agree on, exactly
// like standard.go's padBytes.
var aesSalt = [4]byte{0x73, 0x41, 0x6C, 0x54}

// ObjectKey implements Algorithm 1 ("Encryption of data using the RC4
// or AES algorithms"), ISO 32000-1 §7.6.2, steps (a)-(d): deriving the
// key actually used to encrypt or decrypt one specific object's strings
// and stream data from the document's overall file encryption key
// (fileKey - see ComputeFileKey) plus that object's own number and
// generation.
//
// This step exists so that two different objects in the same file are
// never encrypted with an identical key even though they share the same
// file encryption key - without it, an attacker who worked out the
// plaintext of one RC4-encrypted object could trivially decrypt every
// other object in the file, since RC4 keystreams reused across two
// messages leak the XOR of the two plaintexts. Revision 5-6 (AES-256)
// does not use this function at all - see hash56.go's doc comment for
// why the file encryption key is used directly there instead.
//
// aes reports whether the object will be encrypted with an AES cipher
// (true) or RC4 (false) - see aesSalt's doc comment for why that changes
// the computation, and Method's doc comment (handler.go) for how a
// Handler decides which one applies to a given object.
func ObjectKey(fileKey []byte, num, gen int, aes bool) []byte {
	h := md5.New()
	h.Write(fileKey)

	// Step (b): the object number's low-order 3 bytes, then the
	// generation number's low-order 2 bytes, both little-endian. PDF
	// object and generation numbers are small integers in every
	// real-world file (this package's caller, internal/parser, already
	// bounds them well below 2^24 and 2^16 respectively - see
	// parser.parseNonNegativeInt), so truncating to these widths, as the
	// algorithm requires, never actually discards any real object's
	// number.
	h.Write([]byte{
		byte(num), byte(num >> 8), byte(num >> 16),
		byte(gen), byte(gen >> 8),
	})

	// Step (c): four extra fixed bytes, but only for AES - RC4-encrypted
	// objects skip this step entirely.
	if aes {
		h.Write(aesSalt[:])
	}

	// Step (d): the object key is the first (len(fileKey) + 5) bytes of
	// the MD5 digest just built, capped at 16 (MD5's entire output
	// length - asking for more than that is impossible, and the
	// algorithm caps it there explicitly).
	sum := h.Sum(nil)
	n := len(fileKey) + 5
	if n > len(sum) {
		n = len(sum)
	}
	return sum[:n]
}
