// internal/utils/response.go
package utils

import (
	"encoding/json"
	"net/http"
	"strings"
)

type ErrorResponse struct {
	Error   string         `json:"error"`
	Code    string         `json:"code"`
	Details map[string]any `json:"details"`
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func WriteError(w http.ResponseWriter, status int, message string) {
	code := http.StatusText(status)
	code = strings.ReplaceAll(strings.ToUpper(code), " ", "_")
	if code == "" {
		code = "UNKNOWN_ERROR"
	}
	resp := ErrorResponse{
		Error:   message,
		Code:    code,
		Details: map[string]any{},
	}
	WriteJSON(w, status, resp)
}