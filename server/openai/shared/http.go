package shared

import (
	"encoding/json"
	"net/http"
)

func WriteJson(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
}

func WriteError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	errorType := "invalid_request"

	if code >= 500 {
		errorType = "server_error"
	}

	resp := ErrorResponse{
		Error: Error{
			Type:    errorType,
			Message: err.Error(),
		},
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(resp)
}
