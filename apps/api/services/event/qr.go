package event

import (
	"errors"
	"fmt"
	"strings"

	"rsc.io/qr"
)

const (
	quietZoneModules = 4 // the QR specification's required blank border
	svgPixelsPerUnit = 8
)

// RenderQR encodes text as a QR code and returns the image bytes and their content type.
func RenderQR(text string, format QRFormat) ([]byte, string, error) {
	if format != QRPNG && format != QRSVG {
		return nil, "", ErrUnsupportedFormat
	}
	if text == "" {
		return nil, "", errors.New("nothing to encode")
	}

	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, "", fmt.Errorf("encode QR code: %w", err)
	}

	if format == QRPNG {
		return code.PNG(), "image/png", nil
	}
	return renderSVG(code), "image/svg+xml", nil
}

// renderSVG draws each dark module as a unit square in one path, inside the quiet zone.
func renderSVG(code *qr.Code) []byte {
	total := code.Size + 2*quietZoneModules
	pixels := total * svgPixelsPerUnit

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges" role="img" aria-label="QR code">`,
		total, total, pixels, pixels)
	b.WriteString(`<rect width="100%" height="100%" fill="#fff"/><path fill="#000" d="`)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d,%dh1v1h-1z", x+quietZoneModules, y+quietZoneModules)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return []byte(b.String())
}
