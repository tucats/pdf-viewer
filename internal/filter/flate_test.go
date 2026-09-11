package filter

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func encodeFlateForTest(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("encoding test Flate data: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing test Flate writer: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeFlateRoundTrip(t *testing.T) {
	data := []byte("BT /F1 12 Tf 100 700 Td (Hello, World) Tj ET")
	encoded := encodeFlateForTest(t, data)
	got, err := decodeFlate(encoded, nil)
	if err != nil {
		t.Fatalf("decodeFlate: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("decodeFlate round trip = %q, want %q", got, data)
	}
}

func TestDecodeFlateInvalidData(t *testing.T) {
	if _, err := decodeFlate([]byte("not a zlib stream"), nil); err == nil {
		t.Fatal("decodeFlate on garbage input: expected an error, got nil")
	}
}

func TestDecodeFlateEmptyStreamTolerated(t *testing.T) {
	got, err := decodeFlate(nil, nil)
	if err != nil {
		t.Fatalf("decodeFlate on empty stream: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("decodeFlate on empty stream = %q, want empty", got)
	}
}

func TestDecodeFlateTruncatedStreamTolerated(t *testing.T) {
	data := []byte("BT /F1 12 Tf 100 700 Td (Hello, World) Tj ET")
	encoded := encodeFlateForTest(t, data)

	// Drop the trailing Adler-32 checksum, as a truncated/malformed
	// producer's stream might: this should still decode the DEFLATE
	// payload that came before it rather than failing outright, matching
	// every other filter's tolerance of a missing terminator.
	truncated := encoded[:len(encoded)-4]

	got, err := decodeFlate(truncated, nil)
	if err != nil {
		t.Fatalf("decodeFlate on truncated stream: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("decodeFlate truncated stream = %q, want %q", got, data)
	}
}
