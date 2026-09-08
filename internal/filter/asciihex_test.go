package filter

import (
	"bytes"
	"testing"
)

func TestDecodeASCIIHex(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"basic", "48656C6C6F>", "Hello", false},
		{"whitespace ignored", "48 65\n6C 6C\t6F>", "Hello", false},
		{"odd digit count pads with implicit zero", "48656C6C6F0>", "Hello\x00", false},
		{"empty", ">", "", false},
		{"no terminator tolerated", "48656C6C6F", "Hello", false},
		{"lowercase digits", "68656c6c6f>", "hello", false},
		{"invalid character", "48G>", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeASCIIHex([]byte(c.in))
			if c.wantErr {
				if err == nil {
					t.Fatalf("decodeASCIIHex(%q): expected an error, got %q", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeASCIIHex(%q): unexpected error: %v", c.in, err)
			}
			if !bytes.Equal(got, []byte(c.want)) {
				t.Fatalf("decodeASCIIHex(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
