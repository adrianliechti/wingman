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

	resp := ErrorResponse{
		Error: Error{
			Code:    code,
			Message: err.Error(),
			Status:  httpStatusToGeminiStatus(code),
		},
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(resp)
}

func httpStatusToGeminiStatus(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	default:
		return "UNKNOWN"
	}
}
