package features_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

// TestMissingEncryptedReasoningCompatibilityHTTP reproduces the request shape
// emitted by older VS Code BYOK clients in stateless mode: the prior reasoning
// item keeps its rs_* ID, but encrypted_content, summary, and content are lost.
//
// OpenAI accepts that replay as a no-op. Wingman must do the same even when its
// upstream provider applies stricter validation to empty reasoning items.
func TestMissingEncryptedReasoningCompatibilityHTTP(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	turn1 := map[string]any{
		"input":   "Count the letter 'e' in 'nevertheless'.",
		"store":   false,
		"include": []string{"reasoning.encrypted_content"},
		"reasoning": map[string]any{
			"effort":  "low",
			"summary": "auto",
		},
	}

	openaiResp1, err := h.Client.Post(ctx, h.OpenAI, "/responses", responses.WithModel(turn1, h.ReferenceModel))
	if err != nil {
		t.Fatalf("openai turn 1 failed: %v", err)
	}
	if openaiResp1.StatusCode != http.StatusOK {
		t.Fatalf("openai turn 1 returned %d: %s", openaiResp1.StatusCode, string(openaiResp1.RawBody))
	}

	brokenReplay := missingEncryptedReasoningReplay(t, requireEncryptedReasoning(t, "openai", openaiResp1.Body))

	t.Run("original_openai_accepts", func(t *testing.T) {
		resp, err := h.Client.Post(ctx, h.OpenAI, "/responses", responses.WithModel(brokenReplay, h.ReferenceModel))
		if err != nil {
			t.Fatalf("openai replay failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected OpenAI to accept the empty reasoning item, got %d: %s", resp.StatusCode, string(resp.RawBody))
		}

		responses.RequireMessageOutput(t, "openai", resp.Body)
	})

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Thinking {
			continue
		}

		t.Run("wingman_skips_"+model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			resp, err := h.Client.Post(ctx, h.Wingman, "/responses", responses.WithModel(brokenReplay, model.Name))
			if err != nil {
				t.Fatalf("wingman replay failed: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected Wingman to skip the empty reasoning item, got %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			responses.RequireMessageOutput(t, "wingman", resp.Body)
		})
	}
}

func missingEncryptedReasoningReplay(t *testing.T, output []any) map[string]any {
	t.Helper()

	input := []any{
		map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "Count the letter 'e' in 'nevertheless'."},
			},
		},
	}

	foundReasoning := false
	for _, item := range output {
		obj, _ := item.(map[string]any)
		if obj["type"] != "reasoning" {
			input = append(input, item)
			continue
		}

		id, _ := obj["id"].(string)
		if id == "" {
			continue
		}

		foundReasoning = true
		input = append(input, map[string]any{
			"type":    "reasoning",
			"id":      id,
			"summary": []any{},
		})
	}

	if !foundReasoning {
		t.Fatal("OpenAI turn 1 returned no reasoning item to corrupt")
	}

	input = append(input, map[string]any{
		"type": "message",
		"role": "user",
		"content": []map[string]any{
			{"type": "input_text", "text": "Are you sure? Recount carefully."},
		},
	})

	return map[string]any{
		"input":   input,
		"store":   false,
		"include": []string{"reasoning.encrypted_content"},
		"reasoning": map[string]any{
			"effort":  "low",
			"summary": "auto",
		},
	}
}
