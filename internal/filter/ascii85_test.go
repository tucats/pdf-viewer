package filter

import (
	"bytes"
	"testing"
)

func TestDecodeASCII85(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"spec example", "9jqo^BlbD-BleB1DJ+*+F(f,q~>", "Man is distinguished", false},
		{"z shorthand", "zzz~>", "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00", false},
		{"empty", "~>", "", false},
		{"no terminator tolerated", "9jqo^", "Man ", false},
		{"whitespace ignored", "9j qo^\n~>", "Man ", false},
		{"leading delimiter accepted", "<~9jqo^~>", "Man ", false},
		{"z inside group is an error", "!!z~>", "", true},
		{"invalid character", "9jqo^\x7f~>", "", true},
		{"truncated single leftover char", "9~>", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeASCII85([]byte(c.in))
			if c.wantErr {
				if err == nil {
					t.Fatalf("decodeASCII85(%q): expected an error, got %q", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeASCII85(%q): unexpected error: %v", c.in, err)
			}
			if !bytes.Equal(got, []byte(c.want)) {
				t.Fatalf("decodeASCII85(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestDecodeASCII85RoundTrip checks every possible final-group length
// (0-4 leftover characters) against data this test encodes itself using
// the same base-85 rule the decoder implements, rather than depending on
// a second, independent ASCII85 encoder.
func TestDecodeASCII85RoundTrip(t *testing.T) {
	for n := 0; n <= 8; n++ {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i*37 + 5)
		}
		encoded := encodeASCII85ForTest(data)
		got, err := decodeASCII85(encoded)
		if err != nil {
			t.Fatalf("n=%d: decodeASCII85(%q): %v", n, encoded, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("n=%d: decodeASCII85(%q) = %v, want %v", n, encoded, got, data)
		}
	}
}

// encodeASCII85ForTest is a minimal, independent-enough ASCII85 encoder
// used only to produce round-trip test input; it deliberately does not
// share code with decodeASCII85, so a bug in the decoder can't be
// masked by an equal-and-opposite bug in the encoder.
func encodeASCII85ForTest(data []byte) []byte {
	var out []byte
	for len(data) >= 4 {
		v := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
		out = append(out, encodeGroup(v, 5)...)
		data = data[4:]
	}
	if len(data) > 0 {
		var buf [4]byte
		copy(buf[:], data)
		v := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
		out = append(out, encodeGroup(v, len(data)+1)...)
	}
	out = append(out, '~', '>')
	return out
}

func encodeGroup(v uint32, n int) []byte {
	var digits [5]byte
	for i := 4; i >= 0; i-- {
		digits[i] = byte(v%85) + '!'
		v /= 85
	}
	return digits[:n]
}
