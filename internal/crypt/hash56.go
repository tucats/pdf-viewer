package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/sha512"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file implements the AES-256 ("V5") Standard Security Handler
// introduced by Adobe's Extension Level 3 and later folded into the
// specification proper as revisions 5 and 6 (ISO 32000-2 §7.6.4.3 and
// §7.6.4.4). It is a genuinely different construction from the
// RC4/AES-128 algorithm in standard.go and key.go, not a variant of it:
//
//   - The file encryption key is not *derived* from the password at all
//     - it is an independently chosen 256-bit key, stored (encrypted
//     under a password-derived wrapping key) in the /UE and /OE
//     dictionary entries. Retrieving it means unwrapping /UE, not
//     recomputing a hash the way ComputeFileKey does.
//   - Because the file key is not password-derived, per-object key
//     mixing (key.go's ObjectKey, needed for RC4/AES-128 so that no two
//     objects share a literal key) serves no purpose here and is not
//     used: every object in a V5 document is encrypted directly with
//     the same 256-bit file key.
//   - Revision 6 additionally "hardens" the hash function itself
//     (hashR6 below) against brute-force password guessing by making
//     each hash deliberately expensive to compute - revision 5 (now
//     obsolete, but some older files still use it) hashes just once,
//     with plain SHA-256.

// zeroIV16 is the all-zero 16-byte initialization vector ISO 32000-2
// §7.6.4.4.7's Algorithm 8 specifies for wrapping and unwrapping the
// file encryption key inside /UE and /OE. Unlike every other use of AES
// in this package (see cipher.go's aesEncryptCBC/aesDecryptCBC, used for
// actual string/stream content), a zero IV is safe here specifically
// because /UE and /OE each wrap exactly one, always-freshly-random file
// key exactly once - the property a reused IV would normally endanger
// (encrypting two different messages under the same key and IV) never
// arises.
var zeroIV16 = make([]byte, 16)

// HashRevision computes the password-hashing function ISO 32000-2
// §7.6.4.4.7's Algorithm 2.A calls "Hash", for either revision 5 (plain
// SHA-256 over password+salt+udata) or revision 6 (the hardened
// algorithm - see hashR6 below). udata is nil when validating or
// deriving a *user* password, or the already-computed 48-byte /U string
// when validating or deriving an *owner* password (Algorithms 9 and
// 2.A's owner-path both fold /U into the hash so that an owner password
// alone cannot be checked without also knowing /U) - see
// ComputeAES256OwnerStrings, this package's only caller of the
// owner-password path.
func HashRevision(r int, password, salt, udata []byte) []byte {
	if r <= 5 {
		h := sha256.New()
		h.Write(password)
		h.Write(salt)
		h.Write(udata)
		return h.Sum(nil)
	}
	return hashR6(password, salt, udata)
}

// hashR6 implements ISO 32000-2 §7.6.4.4.7's Algorithm 2.B, the
// "hardened" hash revision 6 uses everywhere revision 5 would have used
// a plain SHA-256. Deliberately making password hashing slow (typically
// a few dozen rounds, each round doing real AES and SHA-2 work) is a
// standard defense against brute-force password guessing - the same
// motivation as standard.go's 50-round MD5 repetition for revisions 3-4,
// just a considerably stronger version of the same idea.
//
// The algorithm, in its own terms:
//
//  1. K starts as SHA-256(password || salt || udata).
//  2. Each round: build K1 = 64 repetitions of (password || K || udata),
//     encrypt K1 with AES-128-CBC (no padding - K1's length is already a
//     multiple of the block size, since password+K+udata's length times
//     64 always is) using the first 16 bytes of K as the key and the
//     next 16 bytes of K as the IV, producing E.
//  3. Sum E's first 16 bytes as an ordinary (not two's-complement)
//     integer; taken mod 3, that selects which hash function - SHA-256,
//     SHA-384, or SHA-512 - hashes E into the next round's K.
//  4. Stop once at least 64 rounds have run AND E's very last byte is no
//     larger than (the round just completed, minus 32); otherwise go
//     back to step 2 with the new K.
//
// The first 32 bytes of the final K is the hash result.
func hashR6(password, salt, udata []byte) []byte {
	k := sha256.Sum256(bytes.Join([][]byte{password, salt, udata}, nil))
	key := k[:]

	round := 0
	for {
		// Step 2: K1 is (password || key || udata) repeated 64 times.
		block := bytes.Join([][]byte{password, key, udata}, nil)
		k1 := bytes.Repeat(block, 64)

		cipherBlock, err := aes.NewCipher(key[:16])
		if err != nil {
			// key is always a hash-function output (32, 48, or 64 bytes),
			// so key[:16] always succeeds and is always a valid AES-128
			// key length; this can only happen if that invariant is
			// broken by a future edit to this function.
			panic("crypt: hashR6 built an invalid AES key: " + err.Error())
		}
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(cipherBlock, key[16:32]).CryptBlocks(e, k1)

		// Step 3: which hash runs next depends on the first 16 bytes of
		// E, summed as plain (not modular-reduced-per-byte) integers.
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		switch sum % 3 {
		case 0:
			h := sha256.Sum256(e)
			key = h[:]
		case 1:
			h := sha512.Sum384(e)
			key = h[:]
		default: // 2
			h := sha512.Sum512(e)
			key = h[:]
		}

		round++
		if round >= 64 && int(e[len(e)-1]) <= round-32 {
			break
		}
	}

	return key[:32]
}

// ComputeFileKeyR56 implements the user-password half of ISO 32000-2
// §7.6.4.4.9's Algorithm 2.A ("Retrieving the file encryption key from
// an encrypted document in order to decrypt it"): given the (here,
// always empty - see the package doc comment) user password and the
// document's /U and /UE entries, it both validates that password and,
// if valid, unwraps and returns the document's 32-byte AES-256 file
// encryption key.
//
// u must be the full 48-byte /U string (32-byte hash, 8-byte validation
// salt, 8-byte key salt) and ue the 32-byte /UE string; New (handler.go)
// is responsible for checking both are actually that length before
// calling this, so a length mismatch here always indicates a bug in this
// package rather than malformed input. ErrWrongPassword is returned
// (wrapping no PDF-file-content error at all) when password does not
// validate against u - see handler.go's doc comment on how that
// distinguishes "this document needs a password we don't have" from an
// actually-corrupt file.
func ComputeFileKeyR56(password []byte, r int, u, ue []byte) ([]byte, error) {
	validationSalt, keySalt := u[32:40], u[40:48]

	if !bytes.Equal(HashRevision(r, password, validationSalt, nil), u[0:32]) {
		return nil, ErrWrongPassword
	}

	intermediateKey := HashRevision(r, password, keySalt, nil)
	fileKey, err := aesCBCNoPad(intermediateKey, zeroIV16, ue, false)
	if err != nil {
		return nil, pdferror.Malformedf("unwrapping /UE: %v", err)
	}
	return fileKey, nil
}

// ComputeAES256UserStrings implements the user-password half of ISO
// 32000-2 §7.6.4.4.7's Algorithm 8 ("Computing the encryption
// dictionary's U (user password) and UE (user encryption) values"): the
// forward direction of ComputeFileKeyR56 above, used only by this
// package's own tests and by tools/genfixtures to build a real,
// spec-correct encrypted fixture (see the package doc comment's "Why
// some of this package's functions are exported" section) - New never
// calls this, since this project only ever reads PDF files, not writes
// them.
//
// validationSalt and keySalt must each be 8 bytes; a real PDF-writing
// application would generate them randomly, but this package's only
// callers want fully reproducible output (see standard.go's
// ComputeUserHash for the same reasoning), so they are supplied by the
// caller rather than generated here.
func ComputeAES256UserStrings(fileKey []byte, r int, validationSalt, keySalt []byte) (u, ue []byte, err error) {
	hash := HashRevision(r, nil, validationSalt, nil)
	u = bytes.Join([][]byte{hash, validationSalt, keySalt}, nil)

	intermediateKey := HashRevision(r, nil, keySalt, nil)
	ue, err = aesCBCNoPad(intermediateKey, zeroIV16, fileKey, true)
	if err != nil {
		return nil, nil, err
	}
	return u, ue, nil
}

// ComputeAES256OwnerStrings implements the owner-password half of
// Algorithm 9 (the /O and /OE counterpart to ComputeAES256UserStrings'
// /U and /UE), again exported only for this package's own tests and
// tools/genfixtures - see ComputeAES256UserStrings' doc comment. u must
// be the already-computed 48-byte /U string, which Algorithm 9 folds
// into the owner hash so that an /O entry alone can never be validated
// without also knowing /U (see HashRevision's udata parameter).
func ComputeAES256OwnerStrings(fileKey []byte, r int, u, validationSalt, keySalt []byte) (o, oe []byte, err error) {
	hash := HashRevision(r, nil, validationSalt, u)
	o = bytes.Join([][]byte{hash, validationSalt, keySalt}, nil)

	intermediateKey := HashRevision(r, nil, keySalt, u)
	oe, err = aesCBCNoPad(intermediateKey, zeroIV16, fileKey, true)
	if err != nil {
		return nil, nil, err
	}
	return o, oe, nil
}
