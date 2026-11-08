package google

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type videoTransport func(*http.Request) (*http.Response, error)

func (f videoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOmniVideoGeneration(t *testing.T) {
	for _, delivery := range []string{"data", "uri"} {
		t.Run(delivery, func(t *testing.T) {
			calls := 0
			deleted := []string{}
			client := &http.Client{Transport: videoTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Header.Get("X-Goog-Api-Key") != "test" {
					t.Error("missing API key")
				}
				response := `{"id":"render-1","status":"completed","steps":[{"type":"model_output","content":[{"type":"video","mime_type":"video/mp4","data":"bXA0"}]}]}`
				if req.Method == "DELETE" {
					deleted = append(deleted, req.URL.Path)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
				} else if req.Method == "POST" {
					if req.URL.Path != "/v1beta/interactions" {
						t.Fatalf("unexpected API: %s", req.URL)
					}
					var body struct {
						Model      string            `json:"model"`
						Store      *bool             `json:"store"`
						Background bool              `json:"background"`
						Input      []map[string]any  `json:"input"`
						Format     map[string]string `json:"response_format"`
					}
					if err := json.UnmarshalRead(req.Body, &body); err != nil {
						t.Fatal(err)
					}
					if body.Model != "gemini-omni-1.2-flash" || body.Store == nil || !*body.Store || body.Background || body.Format["type"] != "video" || body.Format["duration"] != "15s" || body.Format["resolution"] != "720p" || body.Format["delivery"] != "uri" {
						t.Fatalf("request=%+v", body)
					}
					if len(body.Input) != 2 || body.Input[0]["type"] != "image" || body.Input[0]["data"] != "cG5n" {
						t.Fatalf("input=%+v", body.Input)
					}
					if delivery == "uri" {
						response = strings.Replace(response, `"data":"bXA0"`, `"uri":"https://generativelanguage.googleapis.com/v1beta/files/render-1:download?alt=media"`, 1)
					}
				} else if req.URL.Path == "/v1beta/files/render-1" {
					response = `{"name":"files/render-1","state":"ACTIVE"}`
				} else {
					if req.URL.Path != "/v1beta/files/render-1:download" {
						t.Fatalf("download=%s", req.URL)
					}
					response = "mp4"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			renderer, _ := NewRenderer("gemini-omni-1.2-flash", WithToken("test"), WithClient(client))
			result, err := renderer.Render(context.Background(), "A fox running", &provider.RenderOptions{
				Duration: new(15 * time.Second), Format: provider.RenderFormatMP4, Resolution: provider.Resolution720p,
				Images: []provider.File{{Content: []byte("png"), ContentType: "image/png"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.ID != "render-1" || result.ContentType != "video/mp4" || string(result.Content) != "mp4" {
				t.Fatalf("result=%+v", result)
			}
			want := 2
			if delivery == "uri" {
				want = 5
			}
			if !slices.Contains(deleted, "/v1beta/interactions/render-1") {
				t.Errorf("interaction not cleaned up: %v", deleted)
			}
			if delivery == "uri" && !slices.Contains(deleted, "/v1beta/files/render-1") {
				t.Errorf("file not cleaned up: %v", deleted)
			}
			if calls != want {
				t.Errorf("calls=%d", calls)
			}
		})
	}
}

func TestOmniRejectsInvalidDuration(t *testing.T) {
	renderer, _ := NewRenderer("gemini-omni-1.2-flash")
	for _, duration := range []time.Duration{0, -time.Second, 2 * time.Second, 1500 * time.Millisecond, 16 * time.Second} {
		if _, err := renderer.Render(context.Background(), "test", &provider.RenderOptions{Duration: &duration}); err == nil {
			t.Errorf("accepted %s", duration)
		}
	}
}

func TestVideoFileNames(t *testing.T) {
	for _, uri := range []string{"files/abc-123", "https://generativelanguage.googleapis.com/v1beta/files/abc-123", "https://generativelanguage.googleapis.com/v1beta/files/abc-123:download?alt=media"} {
		name, err := videoFileName(uri)
		if err != nil || name != "files/abc-123" {
			t.Errorf("%s: %q, %v", uri, name, err)
		}
	}
	for _, uri := range []string{"", "https://example.com/no-file", "files/", "files/../abc", "files/abc/def"} {
		if name, err := videoFileName(uri); err == nil {
			t.Errorf("accepted %q: %q", uri, name)
		}
	}
}

func TestWaitVideoFile(t *testing.T) {
	for _, scenario := range []string{"ready", "failed", "cancelled", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client, err := (&Config{token: "test", client: &http.Client{Transport: videoTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path != "/v1beta/files/abc-123" {
					t.Fatalf("path=%s", req.URL.Path)
				}
				state := "PROCESSING"
				switch scenario {
				case "ready":
					if calls > 1 {
						state = "ACTIVE"
					}
				case "failed":
					state = "FAILED"
				case "cancelled":
					cancel()
				case "unknown":
					state = "STATE_UNSPECIFIED"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"name":"files/abc-123","state":%q,"error":{"message":"render failed"}}`, state)))}, nil
			})}}).newClient(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = waitVideoFile(ctx, client, "files/abc-123", time.Millisecond)
			switch scenario {
			case "ready":
				if err != nil || calls != 2 {
					t.Fatalf("calls=%d, err=%v", calls, err)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
			case "failed":
				if err == nil || !strings.Contains(err.Error(), "render failed") {
					t.Fatalf("err=%v", err)
				}
			default:
				if err == nil {
					t.Fatal("expected invalid-state error")
				}
			}
		})
	}
}

func TestOmniCleansUpAfterDownloadFailure(t *testing.T) {
	cleaned := []string{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: videoTransport(func(req *http.Request) (*http.Response, error) {
		response := `{"id":"render-1","status":"completed","steps":[{"type":"model_output","content":[{"type":"video","mime_type":"video/mp4","uri":"files/abc-123"}]}]}`
		if req.Method == "DELETE" {
			if req.Context().Err() != nil {
				t.Errorf("cleanup inherited cancellation: %v", req.Context().Err())
			}
			cleaned = append(cleaned, req.URL.Path)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		if req.Method == "GET" {
			cancel()
			return nil, context.Canceled
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	renderer, _ := NewRenderer("gemini-omni-1.2-flash", WithToken("test"), WithClient(client))
	if _, err := renderer.Render(ctx, "fox", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if !slices.Equal(cleaned, []string{"/v1beta/files/abc-123", "/v1beta/interactions/render-1"}) {
		t.Errorf("cleanup=%v", cleaned)
	}
}

func TestOmniCleanupFailurePreservesVideo(t *testing.T) {
	client := &http.Client{Transport: videoTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "DELETE" {
			return &http.Response{StatusCode: 403, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"permission_denied","message":"denied"}}`))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"render-1","status":"completed","steps":[{"type":"model_output","content":[{"type":"video","mime_type":"video/mp4","data":"bXA0"}]}]}`))}, nil
	})}
	renderer, _ := NewRenderer("gemini-omni-1.2-flash", WithToken("test"), WithClient(client))
	result, err := renderer.Render(context.Background(), "fox", nil)
	if err != nil || result == nil || string(result.Content) != "mp4" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
