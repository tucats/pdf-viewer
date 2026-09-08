package filter

import (
	"bytes"
	"testing"
)

func TestDecodeRunLength(t *testing.T) {
	cases := []struct {
		name    string
		in      []byte
		want    []byte
		wantErr bool
	}{
		{"literal run", []byte{2, 'a', 'b', 'c', 128}, []byte("abc"), false},
		{"repeat run", []byte{257 - 5, 'x', 128}, bytes.Repeat([]byte("x"), 5), false},
		{"eod stops early", []byte{1, 'a', 'b', 128, 0, 'z'}, []byte("ab"), false},
		{"missing eod tolerated", []byte{0, 'q'}, []byte("q"), false},
		{"truncated literal run", []byte{5, 'a'}, nil, true},
		{"truncated repeat run", []byte{255}, nil, true},
		{"empty input", []byte{}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeRunLength(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("decodeRunLength(%v): expected an error, got %v", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeRunLength(%v): unexpected error: %v", c.in, err)
			}
			if !bytes.Equal(got, c.want) {
				t.Fatalf("decodeRunLength(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
