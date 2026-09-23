package strip

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"golang.org/x/image/webp"
)

// webpKeep lists the RIFF chunks that affect rendering; EXIF, XMP and unknown chunks are dropped.
var webpKeep = map[string]bool{
	"VP8 ": true, "VP8L": true, "VP8X": true, "ALPH": true, "ICCP": true, "ANIM": true, "ANMF": true,
}

const (
	vp8xFlagXMP  = 0x04
	vp8xFlagEXIF = 0x08
)

// cleanWebP rebuilds the RIFF container from the allow-listed chunks, clears the VP8X EXIF/XMP flags,
// fixes the RIFF size and drops anything after the declared payload.
func cleanWebP(b []byte) ([]byte, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil, ErrMalformed
	}
	size := int(binary.LittleEndian.Uint32(b[4:8]))
	if size < 4 || 8+size > len(b) {
		return nil, fmt.Errorf("%w: RIFF size does not match the file", ErrMalformed)
	}
	payload := b[12 : 8+size]

	body := make([]byte, 0, len(payload))
	for i := 0; i < len(payload); {
		if i+8 > len(payload) {
			return nil, fmt.Errorf("%w: truncated chunk header", ErrMalformed)
		}
		kind := string(payload[i : i+4])
		length := int(binary.LittleEndian.Uint32(payload[i+4:]))
		end := i + 8 + length + length%2
		if length < 0 || end > len(payload) {
			return nil, fmt.Errorf("%w: chunk overruns the file", ErrMalformed)
		}
		if webpKeep[kind] {
			chunk := append([]byte(nil), payload[i:end]...)
			if kind == "VP8X" && length >= 1 {
				chunk[8] &^= vp8xFlagEXIF | vp8xFlagXMP
			}
			body = append(body, chunk...)
		}
		i = end
	}

	out := make([]byte, 0, 12+len(body))
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(4+len(body)))
	out = append(out, "WEBP"...)
	out = append(out, body...)
	if _, err := webp.DecodeConfig(bytes.NewReader(out)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return out, nil
}
