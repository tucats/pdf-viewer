package jpx

import (
	"bytes"
	"testing"
)

func TestParseContainerExtractsCodestreamAndColor(t *testing.T) {
	cs := buildCodestream(10, 10, 1, 1, defaultTileConfig, []byte{0x01})
	jp2 := buildJP2File(cs, 10, 10, 1, 7, EnumCSSRGB)

	if !looksLikeJP2(jp2) {
		t.Fatalf("looksLikeJP2 = false, want true")
	}

	info, err := parseContainer(jp2)
	if err != nil {
		t.Fatalf("parseContainer: %v", err)
	}
	if !bytes.Equal(info.codestream, cs) {
		t.Errorf("extracted codestream does not match what was embedded")
	}
	if info.height != 10 || info.width != 10 {
		t.Errorf("ihdr height/width = %d/%d, want 10/10", info.height, info.width)
	}
	if info.componentCount != 1 {
		t.Errorf("ihdr componentCount = %d, want 1", info.componentCount)
	}
	if info.colorSpaceMethod != 1 || info.enumeratedColorSpace != EnumCSSRGB {
		t.Errorf("colr method/enumCS = %d/%d, want 1/%d", info.colorSpaceMethod, info.enumeratedColorSpace, EnumCSSRGB)
	}
}

func TestParseContainerLastBoxLengthZero(t *testing.T) {
	cs := buildCodestream(4, 4, 1, 1, defaultTileConfig, []byte{0x01})

	var out []byte
	out = append(out, jp2SignatureBox[:]...)
	out = append(out, jp2Box("ftyp", []byte("jp2 \x00\x00\x00\x00jp2 "))...)
	out = append(out, jp2SuperBox("jp2h",
		jp2Box("ihdr", buildIhdr(4, 4, 1, 7)),
		jp2Box("colr", buildColrEnumerated(EnumCSGreyscale)),
	)...)
	// A length-0 "jp2c" box: its length field is 0, meaning "runs to the
	// end of the data" rather than being computed from its own size.
	out = u32(out, 0)
	out = append(out, "jp2c"...)
	out = append(out, cs...)

	info, err := parseContainer(out)
	if err != nil {
		t.Fatalf("parseContainer: %v", err)
	}
	if !bytes.Equal(info.codestream, cs) {
		t.Errorf("length-0 jp2c box: extracted codestream does not match")
	}
}

func TestParseContainerExtendedLengthBox(t *testing.T) {
	inner := []byte("hello")
	var out []byte
	out = u32(out, 1) // LBox == 1: an 8-byte XLBox follows
	out = append(out, "test"...)
	xlLen := uint64(boxHeaderSizeXL + len(inner))
	xl := make([]byte, 8)
	for i := 0; i < 8; i++ {
		xl[7-i] = byte(xlLen >> (8 * i))
	}
	out = append(out, xl...)
	out = append(out, inner...)

	boxes, err := readBoxes(out)
	if err != nil {
		t.Fatalf("readBoxes: %v", err)
	}
	if len(boxes) != 1 || boxes[0].tagString() != "test" || !bytes.Equal(boxes[0].content, inner) {
		t.Errorf("readBoxes result = %+v, want one 'test' box containing %q", boxes, inner)
	}
}

func TestParseContainerMalformed(t *testing.T) {
	validCS := buildCodestream(4, 4, 1, 1, defaultTileConfig, []byte{0x01})
	validJP2 := buildJP2File(validCS, 4, 4, 1, 7, EnumCSGreyscale)

	tests := []struct {
		name    string
		mutate  func([]byte) []byte
		wantErr bool
	}{
		{
			name: "box header truncated",
			mutate: func(b []byte) []byte {
				return append(b, 0x00, 0x00, 0x00)
			},
			wantErr: true,
		},
		{
			name: "box length too small",
			mutate: func(b []byte) []byte {
				// Corrupt the "ftyp" box's length (right after the fixed
				// 12-byte signature box) to something below the 8-byte
				// header size.
				out := append([]byte{}, b...)
				out[12], out[13], out[14], out[15] = 0, 0, 0, 3
				return out
			},
			wantErr: true,
		},
		{
			name: "box claims more content than remains",
			mutate: func(b []byte) []byte {
				out := append([]byte{}, b...)
				out[12], out[13], out[14], out[15] = 0xFF, 0xFF, 0xFF, 0xFF
				return out
			},
			wantErr: true,
		},
		{
			name: "no jp2c box",
			mutate: func(b []byte) []byte {
				return bytes.Replace(b, []byte("jp2c"), []byte("xxxx"), 1)
			},
			wantErr: true,
		},
		{
			name: "no ihdr box",
			mutate: func(b []byte) []byte {
				return bytes.Replace(b, []byte("ihdr"), []byte("xxxx"), 1)
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.mutate(append([]byte{}, validJP2...))
			_, err := parseContainer(data)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseContainer: err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
