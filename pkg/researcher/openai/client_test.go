package openai

import (
	"encoding/json"
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
