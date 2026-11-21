package google

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestSynthesizeMusic(t *testing.T) {
	for _, tt := range []struct {
		name, format, upstreamMIME, contentType, upstreamID string
	}{
		{"default MP3", "", "audio/mp3", "audio/mpeg", ""},
		{"explicit MP3", "mp3", "audio/mpeg", "audio/mpeg", "song-id"},
		{"WAV", "wav", "audio/wav", "audio/wav", "song-id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1beta/interactions" {
					t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("x-goog-api-key") != "test-token" || r.Header.Get("Api-Revision") != "2026-05-20" {
					t.Error("missing API key or Interactions API revision")
				}
				var body struct {
					Model  string            `json:"model"`
					Input  string            `json:"input"`
					Store  *bool             `json:"store"`
					Stream bool              `json:"stream"`
					Format map[string]string `json:"response_format"`
				}
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body.Model != "lyria-3.5" || body.Input != "Have a wonderful day!" || body.Store == nil || *body.Store || body.Stream {
					t.Errorf("unexpected request: %+v", body)
				}
				if tt.format == "wav" {
					if body.Format["type"] != "audio" || body.Format["mime_type"] != "audio/wav" {
						t.Errorf("expected a WAV response format, got %v", body.Format)
					}
				} else if body.Format != nil {
					t.Errorf("expected the upstream MP3 default, got %v", body.Format)
				}
				id := ""
				if tt.upstreamID != "" {
					id = fmt.Sprintf(`"id":%q,`, tt.upstreamID)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{%s"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"lyrics"},{"type":"audio","mime_type":%q,"data":"c29uZw=="}]}]}`, id, tt.upstreamMIME)
			})
			s, err := NewSynthesizer("models/lyria-3.5", WithToken("test-token"), WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			options := &provider.SynthesizeOptions{Format: tt.format, Voice: "alloy"}
			results, err := collect(t, s, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Fatalf("got %d results, want one complete track", len(results))
			}
			result := results[0]
			if result.ID == "" || (tt.upstreamID != "" && result.ID != tt.upstreamID) || result.Model != "models/lyria-3.5" || result.ContentType != tt.contentType || string(result.Content) != "song" {
				t.Errorf("unexpected result: %+v", result)
			}
		})
	}
}

func TestSynthesizeMusicErrors(t *testing.T) {
	for _, tt := range []struct {
		name, format, body, message string
		upstreamStatus, status      int
	}{
		{"unsupported format", "opus", "", "unsupported response_format", 0, http.StatusBadRequest},
		{"no audio", "", `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"lyrics"}]}]}`, "no audio data", http.StatusOK, http.StatusBadGateway},
		{"invalid audio", "", `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"audio","data":"???"}]}]}`, "invalid base64 media", http.StatusOK, http.StatusBadGateway},
		{"failed interaction", "", `{"status":"failed","errors":[{"message":"generation failed"}]}`, "generation failed", http.StatusOK, http.StatusBadGateway},
		{"upstream rate limit", "", `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"rate limit reached"}}`, "rate limit reached", http.StatusTooManyRequests, http.StatusTooManyRequests},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) > 1 {
					t.Error("music generation was retried")
				}
				if tt.upstreamStatus == 0 {
					t.Error("invalid request reached upstream")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.upstreamStatus)
				io.WriteString(w, tt.body)
			})
			s, err := NewSynthesizer("lyria-3.5", WithToken("test-token"), WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			results, err := collect(t, s, &provider.SynthesizeOptions{Format: tt.format})
			if len(results) != 0 || err == nil || !strings.Contains(err.Error(), tt.message) || provider.CodeFromError(err, http.StatusBadGateway) != tt.status {
				t.Errorf("got %d results and error %v, want %d %q", len(results), err, tt.status, tt.message)
			}
		})
	}
}
