package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}`))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Research(t.Context(), "a question", nil)
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
