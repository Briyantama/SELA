package strip

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image/png"
)

// pngKeep lists the chunks that affect rendering. Everything else, eXIf and the text chunks included,
// is dropped. Animation chunks are dropped too; the default image still renders.
var pngKeep = map[string]bool{
	"IHDR": true, "PLTE": true, "IDAT": true, "IEND": true,
	"tRNS": true, "gAMA": true, "cHRM": true, "sRGB": true, "iCCP": true,
	"sBIT": true, "bKGD": true, "pHYs": true, "hIST": true,
}

// cleanPNG keeps the allow-listed chunks, verifies every CRC, and drops anything after IEND.
func cleanPNG(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, pngSignature) {
		return nil, ErrMalformed
	}
	out := make([]byte, 0, len(b))
	out = append(out, pngSignature...)
	for i := len(pngSignature); i+12 <= len(b); {
		length := int(binary.BigEndian.Uint32(b[i:]))
		end := i + 12 + length
		if length < 0 || end > len(b) {
			return nil, fmt.Errorf("%w: chunk overruns the file", ErrMalformed)
		}
		kind := string(b[i+4 : i+8])
		if crc32.ChecksumIEEE(b[i+4:end-4]) != binary.BigEndian.Uint32(b[end-4:end]) {
			return nil, fmt.Errorf("%w: bad %q checksum", ErrMalformed, kind)
		}
		if pngKeep[kind] {
			out = append(out, b[i:end]...)
		}
		if kind == "IEND" {
			if _, err := png.DecodeConfig(bytes.NewReader(out)); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
			}
			return out, nil
		}
		i = end
	}
	return nil, fmt.Errorf("%w: no IEND chunk", ErrMalformed)
}
