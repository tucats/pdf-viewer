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
