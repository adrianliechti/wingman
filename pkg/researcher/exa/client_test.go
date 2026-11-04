package exa

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/researcher"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestResearchStreamAndJSONFallback(t *testing.T) {
	for _, streaming := range []bool{true, false} {
		t.Run(map[bool]string{true: "stream", false: "json"}[streaming], func(t *testing.T) {
			client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				var body SearchRequest
				if err := json.UnmarshalRead(req.Body, &body); err != nil {
					t.Fatal(err)
				}
				if !body.Stream || body.Type != "deep-reasoning" || body.OutputSchema.Type != "text" {
					t.Fatalf("request = %+v", body)
				}
				if !strings.Contains(body.SystemPrompt, "source restrictions") || !strings.Contains(body.SystemPrompt, "Research task: q") {
					t.Fatalf("missing synthesis instructions: %+v", body)
				}
				mediaType, data := "application/json", `{"output":{"content":"Report.","grounding":[{"citations":[{"url":"https://example.com","title":"MCP"}]}]}}`
				if streaming {
					mediaType = "text/event-stream"
					// Event shapes as streamed by Exa, including a discarded draft.
					data = `data: {"type":"text-delta","delta":"Draft [3]","choices":[{"index":0,"delta":{"content":"Draft [3]"},"finish_reason":null}]}

data: {"type":"stream-reset","streamReset":true}

data: {"type":"text-delta","delta":"Report.","choices":[{"index":0,"delta":{"content":"Report."},"finish_reason":null}]}

data: {"type":"grounding","grounding":[{"field":"content","citations":[{"url":"https://example.com","title":"MCP"}]}],"citations":[{"url":"https://example.com","title":"MCP"}]}

data: {"type":"done","output":{"content":"Report.","grounding":[{"field":"content","citations":[{"url":"https://example.com","title":"MCP"},{"url":"https://example.com","title":"MCP"}]}]},"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {mediaType}}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})}))
			var progress []string
			var report string
			for result, err := range client.Research(t.Context(), "q", nil) {
				if err != nil {
					t.Fatal(err)
				}
				if result.Progress != "" {
					progress = append(progress, result.Progress)
				} else {
					report = result.Content
				}
			}
			if len(progress) < 2 || report != "Report.\n\nSources:\n- MCP: https://example.com" {
				t.Fatalf("progress = %v, report = %q", progress, report)
			}
		})
	}
}

func TestResearchRejectsTruncatedStream(t *testing.T) {
	client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`data: {"type":"text-delta","delta":"partial"}` + "\n\n"))}, nil
	})}))
	if result, err := researcher.Collect(client.Research(t.Context(), "q", nil)); err == nil || result != nil {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}
