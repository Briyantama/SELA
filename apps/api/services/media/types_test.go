package media

import (
	"errors"
	"testing"
)

func TestKindOf_acceptsTheDocumentedFormats(t *testing.T) {
	tests := []struct {
		contentType string
		wantKind    Kind
		wantType    string
	}{
		{"image/jpeg", KindPhoto, "image/jpeg"},
		{"image/png", KindPhoto, "image/png"},
		{"image/heic", KindPhoto, "image/heic"},
		{"image/webp", KindPhoto, "image/webp"},
		{"video/mp4", KindVideo, "video/mp4"},
		{"video/quicktime", KindVideo, "video/quicktime"},
		{"IMAGE/JPEG", KindPhoto, "image/jpeg"},
		{" video/mp4 ; codecs=avc1", KindVideo, "video/mp4"},
	}

	for _, tc := range tests {
		t.Run(tc.contentType, func(t *testing.T) {
			// Act
			kind, normalized, err := KindOf(tc.contentType)

			// Assert
			if err != nil {
				t.Fatalf("KindOf(%q) error = %v", tc.contentType, err)
			}
			if kind != tc.wantKind || normalized != tc.wantType {
				t.Fatalf("KindOf(%q) = %s, %q; want %s, %q", tc.contentType, kind, normalized, tc.wantKind, tc.wantType)
			}
		})
	}
}

func TestKindOf_rejectsEverythingElse(t *testing.T) {
	for _, contentType := range []string{"", "image/gif", "image/svg+xml", "video/webm", "application/pdf", "text/html", "image"} {
		t.Run(contentType, func(t *testing.T) {
			// Act
			_, _, err := KindOf(contentType)

			// Assert
			if !errors.Is(err, ErrUnsupportedType) {
				t.Fatalf("KindOf(%q) error = %v, want ErrUnsupportedType", contentType, err)
			}
		})
	}
}

func TestMedia_servableOnlyWhenReadyAndActive(t *testing.T) {
	tests := []struct {
		processing ProcessingState
		status     Status
		want       bool
	}{
		{ProcessingReady, StatusActive, true},
		{ProcessingPending, StatusActive, false},
		{ProcessingFailed, StatusActive, false},
		{ProcessingReady, StatusHidden, false},
		{ProcessingReady, StatusDeleted, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.processing)+"/"+string(tc.status), func(t *testing.T) {
			// Arrange
			m := Media{Processing: tc.processing, Status: tc.status}

			// Act
			got := m.Servable()

			// Assert
			if got != tc.want {
				t.Fatalf("Servable() = %v, want %v", got, tc.want)
			}
		})
	}
}
