package server

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteUnauthorized(t *testing.T) {
	for path, want := range map[string]string{
		"/v1/messages":                     `{"error":{"message":"invalid or missing credentials","type":"authentication_error"},"type":"error"}`,
		"/v1beta/models/x:generateContent": `{"error":{"code":401,"message":"invalid or missing credentials","status":"UNAUTHENTICATED"}}`,
		"/v1/chat/completions":             `{"error":{"code":"invalid_api_key","message":"invalid or missing credentials","type":"invalid_request_error"}}`,
	} {
		rec := httptest.NewRecorder()
		writeUnauthorized(rec, httptest.NewRequest(http.MethodPost, path, nil))

		var got any
		if rec.Code != http.StatusUnauthorized || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("%s: HTTP %d: %s", path, rec.Code, rec.Body.String())
		}

		if body, _ := json.Marshal(got, json.Deterministic(true)); string(body) != want {
			t.Errorf("%s: got %s, want %s", path, body, want)
		}
	}
}
