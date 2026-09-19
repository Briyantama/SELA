// Package health exposes the liveness endpoint used by local tooling and deploys.
package health

import (
	"net/http"

	"github.com/Briyantama/SELA/internal/httpx"
)

// Handler reports that the process is up. It checks no dependencies.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		httpx.WriteSuccess(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}
