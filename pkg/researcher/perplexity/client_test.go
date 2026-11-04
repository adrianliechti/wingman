package perplexity

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

func TestResearchStream(t *testing.T) {
	var body map[string]any
	stream := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"private reasoning"}}]}

data: {"citations":["https://example.com/one"],"search_results":[{"url":"https://example.com/one","title":"Source one"}],"choices":[]}

data: {"choices":[{"index":0,"delta":{"content":"<think>private"}}]}

data: {"choices":[{"index":0,"delta":{"content":" reasoning</think>Report [1]."}}]}

data: {"citations":["https://example.com/one"],"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	client, _ := New("test", WithModel("configured-model"), WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.UnmarshalRead(req.Body, &body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
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
	if report != "Report [1].\n\nSources:\n[1] Source one: https://example.com/one" {
		t.Fatalf("report = %q", report)
	}
	if strings.Contains(strings.Join(progress, " "), "private") {
		t.Fatal("reasoning leaked into progress")
	}
	if strings.Count(strings.Join(progress, "|"), "Found 1 research sources") != 1 {
		t.Fatalf("repeated source progress: %v", progress)
	}
	if body["model"] != "configured-model" || body["stream"] != true {
		t.Fatalf("request = %+v", body)
	}
}

func TestResearchRejectsTruncatedStream(t *testing.T) {
	for _, ending := range []string{"", `,"finish_reason":"length"`} {
		client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			data := `data: {"choices":[{"index":0,"delta":{"content":"partial"}` + ending + `}]}` + "\n\n"
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(data))}, nil
		})}))
		if result, err := researcher.Collect(client.Research(t.Context(), "q", nil)); err == nil || result != nil {
			t.Fatalf("result = %+v, error = %v", result, err)
		}
	}
}

func TestRemoveTagsPreservesReportMarkup(t *testing.T) {
	if got := removeTags("<think>reasoning</think><table>report</table>"); got != "<table>report</table>" {
		t.Fatal(got)
	}
	if got := removeTags("report<think>unfinished reasoning"); got != "report" {
		t.Fatal(got)
	}
}
