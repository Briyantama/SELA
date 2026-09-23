package strip_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"strconv"
	"testing"

	"golang.org/x/image/webp"

	"github.com/Briyantama/SELA/services/media/strip"
)

func TestSniff_identifiesSupportedFormatsByMagicBytes(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want string
		ok   bool
	}{
		{"jpeg", jpegWithMetadata(t, 4, 2, 1), "image/jpeg", true},
		{"png", pngWithMetadata(t), "image/png", true},
		{"webp", minimalWebP(t), "image/webp", true},
		{"gif is not accepted", []byte("GIF89a\x01\x00\x01\x00"), "", false},
		{"html pretending to be anything", []byte("<html><script>alert(1)</script>"), "", false},
		{"riff but not webp", []byte("RIFF\x04\x00\x00\x00WAVE"), "", false},
		{"empty", nil, "", false},
		{"truncated jpeg magic", []byte{0xFF, 0xD8}, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := strip.Sniff(tc.body)

			// Assert
			if got != tc.want || ok != tc.ok {
				t.Fatalf("Sniff = %q, %t; want %q, %t", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestClean_rejectsAMismatchBetweenDeclaredTypeAndContent(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		body     []byte
	}{
		{"png declared as jpeg", "image/jpeg", pngWithMetadata(t)},
		{"jpeg declared as webp", "image/webp", jpegWithMetadata(t, 4, 2, 1)},
		{"script declared as png", "image/png", []byte("<script>alert(1)</script>")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, err := strip.Clean(tc.declared, tc.body)

			// Assert
			if !errors.Is(err, strip.ErrMismatch) {
				t.Fatalf("err = %v, want ErrMismatch", err)
			}
		})
	}
}

func TestClean_rejectsUnsupportedDeclaredTypes(t *testing.T) {
	for _, declared := range []string{"image/heic", "video/mp4", "text/html"} {
		t.Run(declared, func(t *testing.T) {
			// Act
			_, err := strip.Clean(declared, jpegWithMetadata(t, 4, 2, 1))

			// Assert
			if !errors.Is(err, strip.ErrUnsupported) {
				t.Fatalf("err = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestClean_rejectsMalformedImagesWithTheRightMagic(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		body     []byte
	}{
		{"jpeg header only", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}},
		{"png signature only", "image/png", []byte("\x89PNG\r\n\x1a\n")},
		{"webp with lying riff size", "image/webp", append([]byte("RIFF\xff\xff\x00\x00WEBP"), minimalWebP(t)[12:]...)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, err := strip.Clean(tc.declared, tc.body)

			// Assert
			if !errors.Is(err, strip.ErrMalformed) {
				t.Fatalf("err = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestClean_removesEveryJPEGMetadataBlockAndTrailer(t *testing.T) {
	// Arrange
	in := jpegWithMetadata(t, 4, 2, 1)

	// Act
	out, err := strip.Clean("image/jpeg", in)

	// Assert
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if bytes.Contains(out, []byte(gpsMarker)) || bytes.Contains(out, []byte("Exif")) {
		t.Fatal("metadata survived the strip")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 4 || cfg.Height != 2 {
		t.Fatalf("decode stripped jpeg = %+v, %v; want 4x2", cfg, err)
	}
	if !bytes.HasSuffix(out, []byte{0xFF, 0xD9}) {
		t.Fatal("stripped jpeg must end at EOI")
	}
}

func TestClean_appliesTheEXIFOrientationBeforeDroppingIt(t *testing.T) {
	tests := []struct {
		orientation uint16
		wantW       int
		wantH       int
	}{
		{1, 4, 2}, {2, 4, 2}, {3, 4, 2}, {4, 4, 2},
		{5, 2, 4}, {6, 2, 4}, {7, 2, 4}, {8, 2, 4},
	}

	for _, tc := range tests {
		t.Run("orientation "+strconv.Itoa(int(tc.orientation)), func(t *testing.T) {
			// Arrange
			in := jpegWithMetadata(t, 4, 2, tc.orientation)

			// Act
			out, err := strip.Clean("image/jpeg", in)

			// Assert
			if err != nil {
				t.Fatalf("Clean: %v", err)
			}
			img, err := jpeg.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if b := img.Bounds(); b.Dx() != tc.wantW || b.Dy() != tc.wantH {
				t.Fatalf("size = %dx%d, want %dx%d", b.Dx(), b.Dy(), tc.wantW, tc.wantH)
			}
			if bytes.Contains(out, []byte(gpsMarker)) {
				t.Fatal("metadata survived the re-encode")
			}
		})
	}
}

func TestClean_rotatesPixelsForOrientation6(t *testing.T) {
	// Arrange: orientation 6 means the stored image must be turned 90° clockwise to display upright.
	// The image is large enough that JPEG chroma subsampling does not smear the corner colours.
	in := jpegWithMetadata(t, 32, 16, 6)

	// Act
	out, err := strip.Clean("image/jpeg", in)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Assert: after a clockwise turn the source's bottom-left (x=0, y=max: no red, full green) lands
	// top-left, and its top-left (no red, no green) lands top-right. A counter-clockwise turn would put
	// the red top-right corner there instead.
	r, g, _, _ := img.At(0, 0).RGBA()
	if int(g>>8)-int(r>>8) < 120 {
		t.Fatalf("top-left = r%d g%d, want green well above red (rotated clockwise)", r>>8, g>>8)
	}
	r, g, _, _ = img.At(img.Bounds().Dx()-1, 0).RGBA()
	if r>>8 > 60 || g>>8 > 60 {
		t.Fatalf("top-right = r%d g%d, want dark (the source's top-left)", r>>8, g>>8)
	}
}

func TestClean_keepsOnlySafePNGChunks(t *testing.T) {
	// Arrange
	in := pngWithMetadata(t)

	// Act
	out, err := strip.Clean("image/png", in)

	// Assert
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	for _, leaked := range []string{gpsMarker, "eXIf", "tEXt", "zTXt", "iTXt", "tIME", "prVt", "TRAILER"} {
		if bytes.Contains(out, []byte(leaked)) {
			t.Fatalf("stripped png still contains %q", leaked)
		}
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("decode stripped png: %v", err)
	}
}

func TestClean_removesWebPMetadataChunksAndFlags(t *testing.T) {
	// Arrange
	in := webpWithMetadata(t)

	// Act
	out, err := strip.Clean("image/webp", in)

	// Assert
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if bytes.Contains(out, []byte(gpsMarker)) || bytes.Contains(out, []byte("EXIF")) || bytes.Contains(out, []byte("XMP ")) {
		t.Fatal("metadata survived the strip")
	}
	if got := binary.LittleEndian.Uint32(out[4:8]); int(got) != len(out)-8 {
		t.Fatalf("RIFF size = %d, want %d", got, len(out)-8)
	}
	if flags := out[20]; flags&0x0C != 0 {
		t.Fatalf("VP8X flags = %#x, EXIF/XMP bits must be cleared", flags)
	}
	if _, err := webp.DecodeConfig(bytes.NewReader(out)); err != nil {
		t.Fatalf("decode stripped webp: %v", err)
	}
}

func TestClean_leavesACleanImageDecodable(t *testing.T) {
	// Arrange
	var clean bytes.Buffer
	if err := png.Encode(&clean, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Act
	out, err := strip.Clean("image/png", clean.Bytes())

	// Assert
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	simple, err := strip.Clean("image/webp", minimalWebP(t))
	if err != nil || !bytes.Equal(simple, minimalWebP(t)) {
		t.Fatalf("a metadata-free simple webp should pass unchanged, got %v", err)
	}
}
