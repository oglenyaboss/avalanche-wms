// Package httputil provides HTTP helpers shared across WMS module handlers:
// the standard JSON response envelope and role-based authorization gates. It
// exists to remove the per-module duplication of these helpers (see issue #39).
package httputil

import (
	"encoding/json"
	"log"
	"net/http"
)

// Envelope is the standard API response shape used by every WMS module:
// {"success": bool, "data": <payload|null>, "error": {"code", "message"} | null}.
type Envelope struct {
	Success bool      `json:"success"`
	Data    any       `json:"data"`
	Error   *APIError `json:"error"`
}

// APIError is the error object carried by Envelope when a request fails.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON serializes payload as JSON with the given status code. An encode
// failure is logged rather than returned, mirroring the existing module helpers.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("httputil.WriteJSON encode: %v", err)
	}
}

// WriteError writes a standard error envelope with the given status, error code,
// and human-readable message.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, Envelope{
		Success: false,
		Data:    nil,
		Error:   &APIError{Code: code, Message: message},
	})
}
