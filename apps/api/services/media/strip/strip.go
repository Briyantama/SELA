// Package strip validates uploaded photos by their magic bytes (FR-SEC.3) and removes every metadata
// block from them before storage, EXIF GPS coordinates in particular (FR-SEC.2, FSD 8.7, 8.11).
//
// It works on the container structure (JPEG segments, PNG chunks, RIFF chunks) with an allow-list of
// what may stay, so unknown or future metadata blocks are dropped too. Only a JPEG whose EXIF asks
// for a rotation is decoded and re-encoded, so the photo keeps looking upright once EXIF is gone.
package strip

import (
	"bytes"
	"errors"
)

var (
	// ErrUnsupported means the declared type is not one this package can clean.
	ErrUnsupported = errors.New("unsupported media type")
	// ErrMismatch means the bytes are not of the declared type.
	ErrMismatch = errors.New("content does not match the declared type")
	// ErrMalformed means the bytes carry the right signature but are not a valid image.
	ErrMalformed = errors.New("malformed image")
	// ErrMetadataRemains is a safety net: the cleaned output still had something to strip.
	ErrMetadataRemains = errors.New("metadata remains after stripping")
)

// maxPixels bounds the only full decode (a JPEG rotation), guarding against decompression bombs. It is
// comfortably above today's 50-megapixel phone sensors.
const maxPixels = 64_000_000

const (
	typeJPEG = "image/jpeg"
	typePNG  = "image/png"
	typeWebP = "image/webp"
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// Sniff identifies a supported image by its leading bytes, ignoring any declared type.
func Sniff(b []byte) (string, bool) {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return typeJPEG, true
	case bytes.HasPrefix(b, pngSignature):
		return typePNG, true
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return typeWebP, true
	}
	return "", false
}

// Clean checks that b really is an image of the declared type and returns it without metadata.
func Clean(declared string, b []byte) ([]byte, error) {
	clean, ok := cleaners[declared]
	if !ok {
		return nil, ErrUnsupported
	}
	if sniffed, ok := Sniff(b); !ok || sniffed != declared {
		return nil, ErrMismatch
	}
	out, err := clean(b)
	if err != nil {
		return nil, err
	}
	// Cleaning is idempotent on clean input, so any change on a second pass means something survived.
	again, err := clean(out)
	if err != nil || !bytes.Equal(again, out) {
		return nil, ErrMetadataRemains
	}
	return out, nil
}

var cleaners = map[string]func([]byte) ([]byte, error){
	typeJPEG: cleanJPEG,
	typePNG:  cleanPNG,
	typeWebP: cleanWebP,
}
