package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Briyantama/SELA/internal/httpx"
)

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *string         `json:"error"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not a JSON envelope: %v (body=%q)", err, rec.Body.String())
	}
	return env
}

func TestWriteSuccess_wrapsPayloadInEnvelope(t *testing.T) {
	// Arrange
	rec := httptest.NewRecorder()

	// Act
	httpx.WriteSuccess(rec, http.StatusCreated, map[string]string{"event_id": "abc"})

	// Assert
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	env := decode(t, rec)
	if !env.Success {
		t.Fatal("success = false, want true")
	}
	if env.Error != nil {
		t.Fatalf("error = %q, want null", *env.Error)
	}
	if string(env.Data) != `{"event_id":"abc"}` {
		t.Fatalf("data = %s, want the payload", env.Data)
	}
}

func TestWriteError_hasNullDataAndMessage(t *testing.T) {
	// Arrange
	rec := httptest.NewRecorder()

	// Act
	httpx.WriteError(rec, http.StatusBadRequest, "name is required")

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	env := decode(t, rec)
	if env.Success {
		t.Fatal("success = true, want false")
	}
	if string(env.Data) != "null" {
		t.Fatalf("data = %s, want null", env.Data)
	}
	if env.Error == nil || *env.Error != "name is required" {
		t.Fatalf("error = %v, want \"name is required\"", env.Error)
	}
}
