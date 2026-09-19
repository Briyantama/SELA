// Package httpx holds the shared HTTP response envelope used by every Sela service.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// envelope is the consistent API response shape:
// success flag, nullable data on error, nullable error message on success.
type envelope struct {
	Success bool    `json:"success"`
	Data    any     `json:"data"`
	Error   *string `json:"error"`
}

// WriteSuccess writes data wrapped in a success envelope.
func WriteSuccess(w http.ResponseWriter, status int, data any) {
	write(w, status, envelope{Success: true, Data: data})
}

// WriteError writes message wrapped in an error envelope with null data.
func WriteError(w http.ResponseWriter, status int, message string) {
	write(w, status, envelope{Success: false, Error: &message})
}

func write(w http.ResponseWriter, status int, env envelope) {
	body, err := json.Marshal(env)
	if err != nil {
		slog.Error("encode response envelope", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		slog.Error("write response body", "error", err)
	}
}
