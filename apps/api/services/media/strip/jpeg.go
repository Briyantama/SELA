package strip

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
)

const (
	markerSOS = 0xDA
	markerEOI = 0xD9
	markerCOM = 0xFE
	markerAPP = 0xE0 // APP0; APPn is 0xE0+n

	reencodeQuality = 92
)

// cleanJPEG keeps only the segments needed to render the image, drops everything after EOI, and
// applies the EXIF orientation to the pixels when it asks for a rotation or flip.
func cleanJPEG(b []byte) ([]byte, error) {
	out, orientation, err := filterJPEG(b)
	if err != nil {
		return nil, err
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if orientation < 2 || orientation > 8 {
		return out, nil
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("%w: image too large to rotate", ErrMalformed)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, orient(img, orientation), &jpeg.Options{Quality: reencodeQuality}); err != nil {
		return nil, fmt.Errorf("re-encode jpeg: %w", err)
	}
	return enc.Bytes(), nil
}

// filterJPEG walks the segment structure, returning the kept bytes and the EXIF orientation (0 if none).
func filterJPEG(b []byte) ([]byte, int, error) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil, 0, ErrMalformed
	}
	out := make([]byte, 0, len(b))
	out = append(out, 0xFF, 0xD8)
	orientation := 0

	for i := 2; i < len(b); {
		if b[i] != 0xFF {
			return nil, 0, fmt.Errorf("%w: expected a marker at %d", ErrMalformed, i)
		}
		for i < len(b) && b[i] == 0xFF { // fill bytes
			i++
		}
		if i >= len(b) {
			break
		}
		marker := b[i]
		i++
		switch {
		case marker == markerEOI:
			return append(out, 0xFF, markerEOI), orientation, nil
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7): // TEM, RSTn: no payload
			out = append(out, 0xFF, marker)
			continue
		}
		if i+2 > len(b) {
			return nil, 0, fmt.Errorf("%w: truncated segment", ErrMalformed)
		}
		end := i + int(binary.BigEndian.Uint16(b[i:]))
		if end > len(b) || end < i+2 {
			return nil, 0, fmt.Errorf("%w: segment overruns the file", ErrMalformed)
		}
		payload := b[i+2 : end]
		if isMetadataSegment(marker, payload) {
			if marker == markerAPP+1 && bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
				orientation = exifOrientation(payload[6:])
			}
			i = end
			continue
		}
		out = append(out, 0xFF, marker)
		out = append(out, b[i:end]...)
		i = end
		if marker == markerSOS {
			next, err := entropyEnd(b, i)
			if err != nil {
				return nil, 0, err
			}
			out = append(out, b[i:next]...)
			i = next
		}
	}
	return nil, 0, fmt.Errorf("%w: no end-of-image marker", ErrMalformed)
}

// isMetadataSegment reports whether an APPn or COM segment should be dropped. Only the segments that
// affect rendering stay: JFIF/JFXX (APP0), an ICC colour profile (APP2) and Adobe's colour transform
// (APP14). EXIF, XMP, IPTC, MPF (which can embed further EXIF-bearing images) and all others go.
func isMetadataSegment(marker byte, payload []byte) bool {
	switch {
	case marker == markerCOM:
		return true
	case marker < markerAPP || marker > markerAPP+15:
		return false
	case marker == markerAPP:
		return !bytes.HasPrefix(payload, []byte("JFIF\x00")) && !bytes.HasPrefix(payload, []byte("JFXX\x00"))
	case marker == markerAPP+2:
		return !bytes.HasPrefix(payload, []byte("ICC_PROFILE\x00"))
	case marker == markerAPP+14:
		return !bytes.HasPrefix(payload, []byte("Adobe"))
	}
	return true
}

// entropyEnd returns the index of the next marker after entropy-coded data starting at i. Inside the
// data 0xFF is followed by 0x00 (stuffing), a restart marker or more fill bytes.
func entropyEnd(b []byte, i int) (int, error) {
	for ; i+1 < len(b); i++ {
		if b[i] != 0xFF {
			continue
		}
		next := b[i+1]
		if next == 0x00 || next == 0xFF || (next >= 0xD0 && next <= 0xD7) {
			continue
		}
		return i, nil
	}
	return 0, fmt.Errorf("%w: scan data runs to the end of the file", ErrMalformed)
}

// exifOrientation reads tag 0x0112 from IFD0 of a TIFF block; 0 when absent or unreadable.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0
	}
	count := int(order.Uint16(tiff[ifd:]))
	for n := range count {
		entry := ifd + 2 + n*12
		if entry+12 > len(tiff) {
			return 0
		}
		if order.Uint16(tiff[entry:]) == 0x0112 && order.Uint16(tiff[entry+2:]) == 3 {
			return int(order.Uint16(tiff[entry+8:]))
		}
	}
	return 0
}

// orient applies an EXIF orientation (2–8) so the pixels display upright without the tag.
func orient(src image.Image, orientation int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			sx, sy := sourcePixel(orientation, x, y, w, h)
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

// sourcePixel maps a destination pixel back to the stored image for each EXIF orientation.
func sourcePixel(orientation, x, y, w, h int) (int, int) {
	switch orientation {
	case 2: // mirrored horizontally
		return w - 1 - x, y
	case 3: // rotated 180°
		return w - 1 - x, h - 1 - y
	case 4: // mirrored vertically
		return x, h - 1 - y
	case 5: // transposed
		return y, x
	case 6: // needs a 90° clockwise turn
		return y, h - 1 - x
	case 7: // transversed
		return w - 1 - y, h - 1 - x
	case 8: // needs a 90° counter-clockwise turn
		return w - 1 - y, x
	}
	return x, y
}
