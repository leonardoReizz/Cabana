package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// APIError is the single error shape every endpoint returns. Keeping it uniform
// is what lets the dashboard — and later the CLI — have one error path.
type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Status is already written, so the response is truncated either way;
		// log it rather than pretending we can recover.
		slog.Error("encode response", "error", err)
	}
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, APIError{Error: code, Message: message})
}

// decodeJSON rejects unknown fields so a typo in a client payload fails loudly
// instead of being silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return false
	}
	return true
}
