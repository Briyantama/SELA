package strip_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// gpsMarker is a string planted inside every metadata block, so a leak is easy to detect.
const gpsMarker = "GPS-SECRET-6.2088S-106.8456E"

// tiffWithOrientationAndGPS builds a little-endian TIFF/EXIF block holding an Orientation tag and a
// GPS IFD whose latitude reference carries gpsMarker.
func tiffWithOrientationAndGPS(orientation uint16) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	b.WriteString("II")
	_ = binary.Write(&b, le, uint16(42))
	_ = binary.Write(&b, le, uint32(8)) // IFD0 at offset 8

	// IFD0: 2 entries (Orientation, GPSInfo pointer), next IFD 0.
	ifd0Size := 2 + 2*12 + 4
	gpsIFD := uint32(8 + ifd0Size)
	_ = binary.Write(&b, le, uint16(2))
	_ = binary.Write(&b, le, uint16(0x0112)) // Orientation
	_ = binary.Write(&b, le, uint16(3))      // SHORT
	_ = binary.Write(&b, le, uint32(1))
	_ = binary.Write(&b, le, orientation)
	_ = binary.Write(&b, le, uint16(0))
	_ = binary.Write(&b, le, uint16(0x8825)) // GPSInfo
	_ = binary.Write(&b, le, uint16(4))      // LONG
	_ = binary.Write(&b, le, uint32(1))
	_ = binary.Write(&b, le, gpsIFD)
	_ = binary.Write(&b, le, uint32(0))

	// GPS IFD: 1 ASCII entry pointing at the marker string.
	markerOffset := gpsIFD + 2 + 12 + 4
	_ = binary.Write(&b, le, uint16(1))
	_ = binary.Write(&b, le, uint16(0x0001)) // GPSLatitudeRef (ab)used to carry the marker
	_ = binary.Write(&b, le, uint16(2))      // ASCII
	_ = binary.Write(&b, le, uint32(len(gpsMarker)+1))
	_ = binary.Write(&b, le, markerOffset)
	_ = binary.Write(&b, le, uint32(0))
	b.WriteString(gpsMarker)
	b.WriteByte(0)
	return b.Bytes()
}

func jpegSegment(marker byte, payload []byte) []byte {
	seg := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

// gradient is a small non-square image whose corners differ, so rotations are observable.
func gradient(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / max(w-1, 1)), G: uint8(y * 255 / max(h-1, 1)), B: 64, A: 255})
		}
	}
	return img
}

// jpegWithMetadata encodes a w×h JPEG and inserts EXIF (orientation + GPS), XMP, IPTC, an MPF APP2,
// a comment and a trailer after EOI, all carrying gpsMarker.
func jpegWithMetadata(t *testing.T, w, h int, orientation uint16) []byte {
	t.Helper()
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, gradient(w, h), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	plain := enc.Bytes()

	var out bytes.Buffer
	out.Write(plain[:2]) // SOI
	out.Write(jpegSegment(0xE1, append([]byte("Exif\x00\x00"), tiffWithOrientationAndGPS(orientation)...)))
	out.Write(jpegSegment(0xE1, []byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta>"+gpsMarker+"</x:xmpmeta>")))
	out.Write(jpegSegment(0xED, []byte("Photoshop 3.0\x008BIM"+gpsMarker)))
	out.Write(jpegSegment(0xE2, []byte("MPF\x00"+gpsMarker)))
	out.Write(jpegSegment(0xFE, []byte("comment "+gpsMarker)))
	out.Write(plain[2:])
	out.WriteString("TRAILER " + gpsMarker)
	return out.Bytes()
}

func pngChunk(kind string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(kind)
	b.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(kind))
	crc.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc.Sum32())
	return b.Bytes()
}

// pngWithMetadata encodes a PNG and inserts eXIf, tEXt, zTXt, iTXt, tIME and a private chunk, plus a
// trailer after IEND.
func pngWithMetadata(t *testing.T) []byte {
	t.Helper()
	var enc bytes.Buffer
	if err := png.Encode(&enc, gradient(4, 3)); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	plain := enc.Bytes()
	ihdrEnd := 8 + 4 + 4 + 13 + 4

	var out bytes.Buffer
	out.Write(plain[:ihdrEnd])
	out.Write(pngChunk("eXIf", tiffWithOrientationAndGPS(1)))
	out.Write(pngChunk("tEXt", []byte("Comment\x00"+gpsMarker)))
	out.Write(pngChunk("zTXt", []byte("Comment\x00\x00"+gpsMarker)))
	out.Write(pngChunk("iTXt", []byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00"+gpsMarker)))
	out.Write(pngChunk("tIME", []byte{0x07, 0xEA, 9, 23, 12, 0, 0}))
	out.Write(pngChunk("prVt", []byte(gpsMarker)))
	out.Write(plain[ihdrEnd:])
	out.WriteString("TRAILER " + gpsMarker)
	return out.Bytes()
}

// minimalWebP is a valid 1×1 lossless WebP (simple format).
func minimalWebP(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatalf("decode webp fixture: %v", err)
	}
	return b
}

func riffChunk(kind string, data []byte) []byte {
	var b bytes.Buffer
	b.WriteString(kind)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	if len(data)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

// webpWithMetadata wraps the minimal image in an extended (VP8X) container flagged for EXIF and XMP and
// adds both chunks, an odd-sized one to exercise padding, and a trailer after the RIFF payload.
func webpWithMetadata(t *testing.T) []byte {
	t.Helper()
	simple := minimalWebP(t)
	vp8l := simple[12:] // the VP8L chunk, header included

	vp8x := make([]byte, 10)
	vp8x[0] = 0x08 | 0x04 // EXIF and XMP flags
	// canvas 1×1: width-1 and height-1 as 24-bit little endian, both zero

	var payload bytes.Buffer
	payload.WriteString("WEBP")
	payload.Write(riffChunk("VP8X", vp8x))
	payload.Write(vp8l)
	payload.Write(riffChunk("EXIF", append([]byte("Exif\x00\x00"), tiffWithOrientationAndGPS(1)...)))
	payload.Write(riffChunk("XMP ", []byte("<x:xmpmeta>"+gpsMarker+"</x:xmpmeta>!")))

	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(payload.Len()))
	out.Write(payload.Bytes())
	out.WriteString("TRAILER " + gpsMarker)
	return out.Bytes()
}
