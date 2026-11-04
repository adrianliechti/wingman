package web

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20

func IsTavily(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/v1/tavily/") }
func IsExa(r *http.Request) bool    { return strings.HasPrefix(r.URL.Path, "/v1/exa/") }

// Credentials normalizes SDK credentials before Wingman's existing authorizers.
// An explicit Authorization header always takes precedence.
func Credentials(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			key := ""
			if IsExa(r) {
				key = r.Header.Get("x-api-key")
			} else if IsTavily(r) && r.Body != nil {
				body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
				r.Body.Close()
				if err != nil {
					WriteError(w, r, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				var credentials struct {
					APIKey string `json:"api_key"`
				}
				if json.Unmarshal(body, &credentials) == nil {
					key = credentials.APIKey
				}
			}
			if key != "" {
				r = r.Clone(r.Context())
				r.Header.Set("Authorization", "Bearer "+key)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, message string) {
	var body any = map[string]any{"error": message}
	if IsTavily(r) {
		body = map[string]any{"detail": map[string]string{"error": message}}
	} else if IsExa(r) {
		tag := "INVALID_REQUEST"
		switch status {
		case http.StatusUnauthorized:
			tag = "UNAUTHORIZED"
		case http.StatusNotFound:
			tag = "NOT_FOUND"
		case http.StatusBadGateway:
			tag = "UPSTREAM_ERROR"
		}
		body = map[string]string{"error": message, "tag": tag, "requestId": uuid.NewString()}
	}
	data, encodeErr := json.Marshal(body)
	if encodeErr != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"error":"could not encode error response"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(data)
}

func WriteJSON(w http.ResponseWriter, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// ReadRequest validates the native API's request schema.
func ReadRequest(w http.ResponseWriter, r *http.Request, target any) error {
	return readRequest(w, r, target, true)
}

// ReadCompatibleRequest decodes supported vendor fields and ignores the rest,
// including unknown nested options, so SDK additions do not break retrieval.
func ReadCompatibleRequest(w http.ResponseWriter, r *http.Request, target any) error {
	return readRequest(w, r, target, false)
}

func readRequest(w http.ResponseWriter, r *http.Request, target any, strict bool) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	var body jsontext.Value
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, maxBodyBytes), &body); err != nil {
		return errors.New("invalid JSON request")
	}
	if body.Kind() != '{' {
		return errors.New("request must be a JSON object")
	}
	if err := json.Unmarshal(body, target, json.RejectUnknownMembers(strict)); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

type Selection struct {
	Model  string `json:"model"`
	APIKey string `json:"api_key"`
}

func (s Selection) Provider(r *http.Request) (string, error) {
	model := r.URL.Query().Get("model")
	if s.Model != "" {
		if model != "" && model != s.Model {
			return "", errors.New("conflicting model selections")
		}
		model = s.Model
	}
	return model, nil
}
