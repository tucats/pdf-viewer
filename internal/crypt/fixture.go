package crypt

import "github.com/tucats/pdf-viewer/internal/pdferror"

// EncryptForFixture is the forward (encrypting) counterpart to
// Handler's private decrypt method, exported only so this package's own
// tests and tools/genfixtures can build a real, spec-correct encrypted
// PDF fixture to read back - see the package doc comment's "Why some of
// this package's functions are exported" section. internal/parser never
// calls this: this project only ever reads PDF files.
//
// iv is only consulted for the AESV2 and AESV3 methods (RC4 needs no
// IV, and MethodIdentity needs neither); a real PDF-writing application
// would generate one randomly per string/stream, but this function's
// only callers want fully reproducible output across regenerations (see
// standard.go's ComputeUserHash for the same reasoning), so the caller
// supplies it instead.
func EncryptForFixture(fileKey []byte, method Method, num, gen int, iv, data []byte) ([]byte, error) {
	switch method {
	case MethodIdentity:
		return data, nil
	case MethodRC4:
		return mustRC4(ObjectKey(fileKey, num, gen, false), data), nil
	case MethodAESV2:
		return aesEncryptCBC(ObjectKey(fileKey, num, gen, true), iv, data)
	case MethodAESV3:
		// Revision 5-6 encrypts directly with the file key - see key.go's
		// ObjectKey doc comment for why no per-object mixing applies.
		return aesEncryptCBC(fileKey, iv, data)
	default:
		return nil, pdferror.Malformedf("crypt: unknown method %d", method)
	}
}
