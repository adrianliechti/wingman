package responses_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
)

func TestWebSearchOpenAIReference(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	body := map[string]any{
		"model": h.ReferenceModel,
		"input": "What is the latest stable Go release? Cite an official source.",
		"tools": []any{
			map[string]any{"type": "web_search"},
		},
	}

	resp, err := h.Client.Post(ctx, h.OpenAI, "/responses", body)
	if err != nil {
		t.Fatalf("openai request failed: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("openai returned status %d: %s", resp.StatusCode, string(resp.RawBody))
	}

	pretty, _ := json.MarshalIndent(resp.Body, "", "  ")
	t.Logf("OpenAI web_search reference response:\n%s", pretty)

	output, ok := resp.Body["output"].([]any)
	if !ok || len(output) == 0 {
		t.Fatalf("output is not a non-empty array; got: %v", resp.Body["output"])
	}

	var sawSearchCall, sawCitation bool
	for _, item := range output {
		obj, _ := item.(map[string]any)
		switch obj["type"] {
		case "web_search_call":
			sawSearchCall = true
			action, _ := obj["action"].(map[string]any)
			if action == nil {
				t.Errorf("web_search_call missing action: %+v", obj)
			}
		case "message":
			content, _ := obj["content"].([]any)
			for _, c := range content {
				cm, _ := c.(map[string]any)
				ann, _ := cm["annotations"].([]any)
				for _, a := range ann {
					am, _ := a.(map[string]any)
					if am["type"] == "url_citation" {
						sawCitation = true
					}
				}
			}
		}
	}

	if !sawSearchCall {
		t.Errorf("expected at least one web_search_call item; saw: %s", pretty)
	}
	if !sawCitation {
		t.Logf("note: no url_citation annotation in response (model may not have cited)")
	}
}
