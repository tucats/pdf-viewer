package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rc4"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file wraps the two ciphers the Standard Security Handler uses -
// RC4 (crypto/rc4) and AES-CBC (crypto/aes plus crypto/cipher) - both
// from the Go standard library, matching this project's "no CGO, no
// third-party dependency" policy. Nothing PDF-specific happens in this
// file; key derivation (which *is* PDF-specific) lives in standard.go
// and hash56.go instead.

// mustRC4 XOR-encrypts (equivalently, decrypts - RC4 is a symmetric
// stream cipher, so running it twice with the same key recovers the
// original bytes) data with key, returning a freshly allocated result
// the same length as data.
//
// If you are new to Go: this is named "must" by the convention used
// throughout the standard library (see, e.g., regexp.MustCompile) for a
// function that panics instead of returning an error when its
// precondition is violated - here, rc4.NewCipher only ever fails for a
// key length outside 1-256 bytes, which this package never passes (see
// ComputeFileKey and ObjectKey, both of which only ever produce keys of
// 5-16 bytes), so treating that as a programmer error worth panicking
// on - rather than plumbing an error return through every RC4 call site
// - keeps this package's own key-derivation math (standard.go) readable
// without cluttering it with error checks that can never actually
// trigger.
func mustRC4(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		panic("crypt: invalid RC4 key length: " + err.Error())
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

// aesEncryptCBC encrypts plaintext with AES in CBC mode, PKCS#7 padding
// (ISO 32000-1 §7.6.2's required padding scheme for AES streams and
// strings), and a randomly-unnecessary-here fixed iv supplied by the
// caller (see EncryptForFixture, this function's only real caller -
// internal/parser never encrypts anything, only decrypts, since writing
// PDF files is outside this project's scope; see the package doc
// comment's "Why some of this package's functions are exported"
// section). The returned bytes are iv followed by the ciphertext, which
// is exactly the on-disk format ISO 32000-1 §7.6.2 specifies: "the
// first 16 bytes of the encrypted stream or string shall be the
// initialization vector".
func aesEncryptCBC(key, iv, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	out := make([]byte, len(iv)+len(padded))
	copy(out, iv)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[len(iv):], padded)
	return out, nil
}

// aesDecryptCBC reverses aesEncryptCBC: data is expected to be a
// 16-byte initialization vector followed by one or more complete AES
// blocks of PKCS#7-padded ciphertext, exactly as ISO 32000-1 §7.6.2
// describes. An empty data (an empty PDF string or a zero-length
// stream, both legal) decrypts to empty bytes with no error - encrypting
// nothing is a no-op, so there is no IV to read in the first place.
func aesDecryptCBC(key, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) < aes.BlockSize {
		return nil, pdferror.Malformedf("AES-encrypted data (%d bytes) is shorter than one block", len(data))
	}
	iv, ciphertext := data[:aes.BlockSize], data[aes.BlockSize:]
	if len(ciphertext) == 0 {
		// An IV with no ciphertext blocks after it is not something a
		// conforming writer produces (there would be nothing to pad),
		// but decrypts unambiguously to empty bytes rather than being
		// treated as an error - tolerating it costs nothing and matches
		// this project's general preference for lenient reading (see
		// internal/parser's package doc comment) over rejecting input
		// that has an obvious, harmless interpretation.
		return nil, nil
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, pdferror.Malformedf("AES-encrypted data length (%d bytes after the IV) is not a multiple of the block size", len(ciphertext))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)

	return pkcs7Unpad(plain)
}

// pkcs7Pad pads data up to a multiple of blockSize using PKCS#7 padding:
// every added byte's value is the number of bytes added, and at least
// one byte of padding is always added (even if len(data) is already a
// multiple of blockSize) so that pkcs7Unpad can always find and remove
// it unambiguously.
func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padLen)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padLen)
	}
	return out
}

// aesCBCNoPad runs AES-CBC with no padding at all - unlike
// aesEncryptCBC/aesDecryptCBC above, which implement the padded form
// ISO 32000-1 §7.6.2 uses for actual string and stream content. It
// exists only for hash56.go's Algorithm 8/9 key-wrapping, which always
// operates on an exact multiple of the AES block size (a 32-byte file
// encryption key) and an explicitly-specified (there, all-zero) iv
// rather than one prepended to the data - see hash56.go's zeroIV16 doc
// comment for why a zero IV is safe in that specific, key-wrapping-only
// context. encrypt selects direction: true to wrap (produce /UE or /OE
// from the file key), false to unwrap (recover the file key from /UE or
// /OE).
func aesCBCNoPad(key, iv, data []byte, encrypt bool) ([]byte, error) {
	if len(data)%aes.BlockSize != 0 {
		return nil, pdferror.Malformedf("AES no-padding input length (%d bytes) is not a multiple of the block size", len(data))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	if encrypt {
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	} else {
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	}
	return out, nil
}

// pkcs7Unpad removes PKCS#7 padding added by pkcs7Pad, validating that
// the padding is well-formed (a non-zero count no larger than the data
// itself) before trusting it - a maliciously or accidentally corrupted
// ciphertext (for example, one decrypted with the wrong key, which this
// package's password validation - see handler.go - should normally
// catch before any string or stream is ever decrypted for real) would
// otherwise silently truncate the result by an arbitrary, wrong amount.
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > len(data) || padLen > aes.BlockSize {
		return nil, pdferror.Malformedf("invalid PKCS#7 padding on decrypted AES data")
	}
	return data[:len(data)-padLen], nil
}
