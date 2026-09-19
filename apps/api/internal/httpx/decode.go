package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

// DecodeJSON reads a strict, size-limited JSON body into dst. It requires an application/json
// content type, rejects unknown fields and trailing data, and on failure writes the error response
// itself and returns false; on success it writes nothing and returns true.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		WriteError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
		case errors.Is(err, io.EOF):
			WriteError(w, http.StatusBadRequest, "request body is empty")
		default:
			WriteError(w, http.StatusBadRequest, "invalid request body")
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}
