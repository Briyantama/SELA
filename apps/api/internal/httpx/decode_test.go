package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Briyantama/SELA/internal/httpx"
)

type payload struct {
	Name string `json:"name"`
}

func decodeRequest(body, contentType string, limit int64) (payload, bool, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	var dst payload
	ok := httpx.DecodeJSON(rec, req, &dst, limit)
	return dst, ok, rec
}

func TestDecodeJSON_readsAValidBody(t *testing.T) {
	tests := []struct{ name, contentType string }{
		{"plain", "application/json"},
		{"with charset", "application/json; charset=utf-8"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok, rec := decodeRequest(`{"name":"sela"}`, tc.contentType, 1024)

			// Assert
			if !ok || got.Name != "sela" {
				t.Fatalf("ok=%v payload=%+v body=%s", ok, got, rec.Body)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("a successful decode must not write a response: %s", rec.Body)
			}
		})
	}
}

func TestDecodeJSON_rejectsBadBodiesWithTheRightStatusAndMessage(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		limit       int64
		wantStatus  int
		wantMessage string
	}{
		{"no content type", `{"name":"x"}`, "", 1024, http.StatusUnsupportedMediaType, "content type must be application/json"},
		{"form encoded", `name=x`, "application/x-www-form-urlencoded", 1024, http.StatusUnsupportedMediaType, "content type must be application/json"},
		{"empty body", ``, "application/json", 1024, http.StatusBadRequest, "request body is empty"},
		{"malformed json", `{"name":`, "application/json", 1024, http.StatusBadRequest, "invalid request body"},
		{"unknown field", `{"name":"x","admin":true}`, "application/json", 1024, http.StatusBadRequest, "invalid request body"},
		{"trailing data", `{"name":"x"}{"name":"y"}`, "application/json", 1024, http.StatusBadRequest, "invalid request body"},
		{"wrong field type", `{"name":5}`, "application/json", 1024, http.StatusBadRequest, "invalid request body"},
		{"oversized", `{"name":"` + strings.Repeat("a", 200) + `"}`, "application/json", 64, http.StatusRequestEntityTooLarge, "request body too large"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, ok, rec := decodeRequest(tc.body, tc.contentType, tc.limit)

			// Assert
			if ok {
				t.Fatal("DecodeJSON accepted a bad body")
			}
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantMessage) {
				t.Errorf("body %s should contain %q", rec.Body, tc.wantMessage)
			}
		})
	}
}
