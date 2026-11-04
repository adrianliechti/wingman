package openai

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/researcher"

	"github.com/openai/openai-go/v3/responses"
)

func TestOutputTextUsesFinalMessageAndItsSources(t *testing.T) {
	var response responses.Response
	err := json.Unmarshal([]byte(`{"output":[
		{"type":"message","phase":"commentary","content":[{"type":"output_text","text":"Working","annotations":[{"type":"url_citation","url":"https://draft.example","title":"Draft"}]}]},
		{"type":"web_search_call","id":"search"},
		{"type":"message","phase":"final_answer","content":[{"type":"output_text","text":"The answer.","annotations":[{"type":"url_citation","url":"https://final.example","title":"Final"}]}]}
	]}`), &response)
	if err != nil {
		t.Fatal(err)
	}
	if got := outputText(&response); got != "The answer.\n\nSources:\n- Final: https://final.example" {
		t.Fatalf("outputText = %q", got)
	}
}

func TestResearchStreamProgressAndCitations(t *testing.T) {
	var requestBody map[string]any
	events := []string{
		`{"type":"response.in_progress"}`,
		`{"type":"response.web_search_call.searching","item_id":"search-1"}`,
		`{"type":"response.output_item.done","item":{"type":"web_search_call","action":{"type":"search","queries":["MCP progress requirements"]}}}`,
		`{"type":"response.output_item.added","item":{"type":"web_search_call","action":{"type":"open_page","url":"https://modelcontextprotocol.io/specification"}}}`,
		`{"type":"response.output_text.delta","delta":"Report"}`,
		`{"type":"response.output_text.delta","delta":" text"}`,
		`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","phase":"final_answer","content":[{"type":"output_text","text":"Report text","annotations":[{"type":"url_citation","title":"MCP","url":"https://modelcontextprotocol.io/specification"}]}]}]}}`,
	}
	client, _ := New("test", WithModel("configured-model"), WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if err := json.UnmarshalRead(request.Body, &requestBody); err != nil {
			t.Fatal(err)
		}
		var body strings.Builder
		for _, event := range events {
			fmt.Fprintf(&body, "data: %s\n\n", event)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body.String()))}, nil
	})}))
	var progress []string
	var report string
	for result, err := range client.Research(t.Context(), "question", nil) {
		if err != nil {
			t.Fatal(err)
		}
		if result.Progress != "" {
			progress = append(progress, result.Progress)
		} else {
			report = result.Content
		}
	}
	want := []string{"Starting research", "Planning research", "Searching the web", "Search queries: MCP progress requirements", "Reading source: https://modelcontextprotocol.io/specification", "Writing research report"}
	if !slices.Equal(progress, want) {
		t.Fatalf("progress = %v", progress)
	}
	if report != "Report text\n\nSources:\n- MCP: https://modelcontextprotocol.io/specification" {
		t.Fatalf("report = %q", report)
	}
	if requestBody["model"] != "configured-model" || requestBody["stream"] != true {
		t.Fatalf("request = %+v", requestBody)
	}
}

func TestResearchStreamRejectsIncompleteResults(t *testing.T) {
	for _, event := range []string{
		`{"type":"response.failed","response":{"error":{"code":"server_error","message":"upstream failure"}}}`,
		`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`,
		`{"type":"response.output_text.delta","delta":"partial report"}`,
	} {
		t.Run(event, func(t *testing.T) {
			client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + event + "\n\n"))}, nil
			})}))
			if result, err := researcher.Collect(client.Research(t.Context(), "q", nil)); err == nil || result != nil {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
		})
	}
}

func TestOutputTextDoesNotFallBackToCommentary(t *testing.T) {
	for _, final := range []string{
		``,
		`,{"type":"message","phase":"final_answer","content":[{"type":"refusal","refusal":"Cannot answer"}]}`,
	} {
		var response responses.Response
		err := json.Unmarshal([]byte(`{"output":[{"type":"message","phase":"commentary","content":[{"type":"output_text","text":"Draft"}]}`+final+`]}`), &response)
		if err != nil {
			t.Fatal(err)
		}
		if got := outputText(&response); got != "" {
			t.Fatalf("outputText = %q", got)
		}
	}
}

func TestOutputTextUsesLastUnphasedMessage(t *testing.T) {
	var response responses.Response
	err := json.Unmarshal([]byte(`{"output":[
		{"type":"message","content":[{"type":"output_text","text":"Working"}]},
		{"type":"message","content":[{"type":"output_text","text":"Answer"}]}
	]}`), &response)
	if err != nil {
		t.Fatal(err)
	}
	if got := outputText(&response); got != "Answer" {
		t.Fatalf("outputText = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestResearchDoesNotImposeToolCallCeiling(t *testing.T) {
	var body map[string]any
	client, err := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if err := json.UnmarshalRead(request.Body, &body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}}

`))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := researcher.Collect(client.Research(t.Context(), "a question", nil))
	if err != nil || result.Content != "Answer" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if _, exists := body["max_tool_calls"]; exists {
		t.Fatal("research request imposed a hard tool-call ceiling")
	}
	if len(body["tools"].([]any)) != 1 {
		t.Fatal("web search missing")
	}
}
