package event_test

import (
	"bytes"
	"image"
	"image/color"
	_ "image/png" // registers the PNG decoder
	"regexp"
	"strconv"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/Briyantama/SELA/services/event"
)

const sampleLink = "https://sela.example.test/Ab3dE5gH"

// decodeQR reads the text out of a QR image the way a phone would.
func decodeQR(t *testing.T, img image.Image) string {
	t.Helper()
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatalf("bitmap: %v", err)
	}
	result, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		t.Fatalf("QR code did not decode: %v", err)
	}
	return result.GetText()
}

var (
	svgViewBox = regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`)
	svgModule  = regexp.MustCompile(`M(\d+),(\d+)h1v1h-1z`)
)

// rasterizeSVG draws the module squares of the SVG onto a white bitmap so a decoder can read them.
func rasterizeSVG(t *testing.T, svg string) image.Image {
	t.Helper()
	box := svgViewBox.FindStringSubmatch(svg)
	if box == nil {
		t.Fatalf("SVG has no viewBox: %.120s", svg)
	}
	size, _ := strconv.Atoi(box[1])
	const scale = 8
	img := image.NewGray(image.Rect(0, 0, size*scale, size*scale))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	modules := svgModule.FindAllStringSubmatch(svg, -1)
	if len(modules) == 0 {
		t.Fatal("SVG has no module squares")
	}
	for _, m := range modules {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		for dy := range scale {
			for dx := range scale {
				img.SetGray(x*scale+dx, y*scale+dy, color.Gray{Y: 0})
			}
		}
	}
	return img
}

func TestRenderQR_pngDecodesBackToTheLink(t *testing.T) {
	// Act
	data, contentType, err := event.RenderQR(sampleLink, event.QRPNG)

	// Assert
	if err != nil {
		t.Fatalf("RenderQR: %v", err)
	}
	if contentType != "image/png" {
		t.Errorf("content type = %q, want image/png", contentType)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not a PNG: %v", err)
	}
	if got := decodeQR(t, img); got != sampleLink {
		t.Fatalf("PNG decodes to %q, want %q", got, sampleLink)
	}
}

func TestRenderQR_svgDecodesBackToTheLink(t *testing.T) {
	// Act
	data, contentType, err := event.RenderQR(sampleLink, event.QRSVG)

	// Assert
	if err != nil {
		t.Fatalf("RenderQR: %v", err)
	}
	if contentType != "image/svg+xml" {
		t.Errorf("content type = %q, want image/svg+xml", contentType)
	}
	if got := decodeQR(t, rasterizeSVG(t, string(data))); got != sampleLink {
		t.Fatalf("SVG decodes to %q, want %q", got, sampleLink)
	}
}

func TestRenderQR_svgIsSelfContainedMarkup(t *testing.T) {
	// Act
	data, _, err := event.RenderQR(sampleLink, event.QRSVG)

	// Assert
	if err != nil {
		t.Fatalf("RenderQR: %v", err)
	}
	svg := string(data)
	for _, want := range []string{`<svg xmlns="http://www.w3.org/2000/svg"`, `</svg>`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("SVG is missing %q", want)
		}
	}
	if bytes.Contains(data, []byte("<script")) || bytes.Contains(data, []byte(sampleLink)) {
		t.Errorf("SVG must not embed scripts or the raw link text: %.200s", svg)
	}
}

func TestRenderQR_rejectsAnUnknownFormat(t *testing.T) {
	// Act
	_, _, err := event.RenderQR(sampleLink, event.QRFormat("gif"))

	// Assert
	if err == nil {
		t.Fatal("expected an error for an unsupported format")
	}
}

func TestRenderQR_rejectsEmptyText(t *testing.T) {
	// Act
	_, _, err := event.RenderQR("", event.QRPNG)

	// Assert
	if err == nil {
		t.Fatal("expected an error when there is nothing to encode")
	}
}
