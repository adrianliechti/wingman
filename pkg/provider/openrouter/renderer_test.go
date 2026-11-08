package openrouter

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestRendererSwitchesImageAndVideoAPIs(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/images":
			w.Write([]byte(`{"data":[{"b64_json":"cG5n","media_type":"image/png"}]}`))
		case "/videos":
			var body map[string]any
			if err := json.UnmarshalRead(r.Body, &body); err != nil {
				t.Fatal(err)
			}
			if body["duration"] != float64(5) || body["resolution"] != "720p" || body["aspect_ratio"] != "16:9" {
				t.Errorf("body=%+v", body)
			}
			refs, ok := body["input_references"].([]any)
			if !ok || len(refs) != 1 {
				t.Errorf("references=%+v", body)
			}
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":"job-1","status":"pending","polling_url":"https://untrusted.invalid/ignored"}`))
		case "/videos/job-1":
			w.Write([]byte(`{"id":"job-1","status":"completed","unsigned_urls":["https://untrusted.invalid/ignored"]}`))
		case "/videos/job-1/content":
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("mp4"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	renderer, _ := NewRenderer("test-model", WithToken("test"), WithClient(server.Client()))
	renderer.url = server.URL
	renderer.videoPollInterval = time.Millisecond
	image, err := renderer.Render(context.Background(), "fox", nil)
	if err != nil || string(image.Content) != "png" {
		t.Fatalf("image=%+v err=%v", image, err)
	}
	video, err := renderer.Render(context.Background(), "fox", &provider.RenderOptions{Format: provider.RenderFormatMP4, Duration: new(5 * time.Second), Resolution: provider.Resolution720p, Aspect: provider.AspectRatio16x9, Images: []provider.File{{Content: []byte("png"), ContentType: "image/png"}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(video.Content) != "mp4" || video.ContentType != "video/mp4" || video.ID != "job-1" {
		t.Errorf("video=%+v", video)
	}
	if len(requests) != 4 {
		t.Errorf("requests=%+v", requests)
	}
}

func TestVideoFailureAndCancellation(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "expired", "pending"} {
		t.Run(status, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(http.StatusAccepted)
				json.MarshalWrite(w, videoJob{ID: "job", Status: status, Error: "blocked"})
			}))
			defer server.Close()
			renderer, _ := NewRenderer("model", WithClient(server.Client()))
			renderer.url = server.URL
			renderer.videoPollInterval = time.Hour
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if status == "pending" {
				time.AfterFunc(20*time.Millisecond, cancel)
			}
			_, err := renderer.Render(ctx, "fox", &provider.RenderOptions{Format: provider.RenderFormatMP4})
			if err == nil {
				t.Fatal("expected error")
			}
			if status == "pending" && !errors.Is(err, context.Canceled) {
				t.Errorf("err=%v", err)
			}
			if calls != 1 {
				t.Errorf("calls=%d", calls)
			}
		})
	}
}
