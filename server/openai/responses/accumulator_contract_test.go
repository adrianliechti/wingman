package responses

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"
	sdk "github.com/openai/openai-go/v3/responses"
)

type responsesContractCompleter struct {
	chunks []provider.Completion
	err    error
}

func (c responsesContractCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for _, chunk := range c.chunks {
			if !yield(&chunk, nil) {
				return
			}
		}
		if c.err != nil {
			yield(nil, c.err)
		}
	}
}

func TestAccumulatorResponsesContract(t *testing.T) {
	content := func(parts ...provider.Content) provider.Completion {
		return provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: parts}}
	}
	call := provider.ToolCallContent(provider.ToolCall{ID: "call_check", Name: "check", Arguments: "{}"})
	for _, tc := range []struct {
		name   string
		chunks []provider.Completion
		err    error
		status string
		types  []string
	}{
		{"fragmented plain text", []provider.Completion{content(provider.TextContent("Hel")), content(provider.TextContent("lo")), {Status: provider.CompletionStatusCompleted}}, nil, "completed", []string{"message"}},
		{"reasoning and tool in one chunk", []provider.Completion{content(provider.ReasoningContent(provider.Reasoning{Summary: provider.TextParts("Check."), Signature: "opaque"}), call), {Status: provider.CompletionStatusCompleted}}, nil, "completed", []string{"reasoning", "function_call"}},
		{"tool then reasoning then text", []provider.Completion{content(call), content(provider.ReasoningContent(provider.Reasoning{Summary: provider.TextParts("Checked."), Signature: "opaque"})), content(provider.TextContent("Done.")), {Status: provider.CompletionStatusCompleted}}, nil, "completed", []string{"function_call", "reasoning", "message"}},
		{"reasoning only cutoff", []provider.Completion{content(provider.ReasoningContent(provider.Reasoning{Summary: provider.TextParts("Still checking.")})), {Status: provider.CompletionStatusIncomplete}}, nil, "incomplete", []string{"reasoning"}},
		{"explicit empty final text", []provider.Completion{content(provider.Content{MessageID: "msg_empty", Phase: provider.MessagePhaseFinalAnswer, TextPresent: true}), {Status: provider.CompletionStatusCompleted}}, nil, "completed", []string{"message"}},
		{"cutoff without output", []provider.Completion{{Status: provider.CompletionStatusIncomplete}}, nil, "incomplete", nil},
		{"tool only after empty deltas", []provider.Completion{content(provider.TextContent("")), content(call), {Status: provider.CompletionStatusCompleted}}, nil, "completed", []string{"function_call"}},
		{"signature only reasoning", []provider.Completion{content(provider.ReasoningContent(provider.Reasoning{Signature: "opaque"})), {Status: provider.CompletionStatusCompleted}}, nil, "completed", []string{"reasoning"}},
		{"unlabelled reasoning tool and text", []provider.Completion{
			content(provider.ReasoningContent(provider.Reasoning{Summary: provider.TextParts("Check the result."), Signature: "opaque"})),
			content(call), content(provider.TextContent("Done.")), {Status: provider.CompletionStatusCompleted},
		}, nil, "completed", []string{"reasoning", "function_call", "message"}},
		{"native message boundaries", []provider.Completion{
			content(provider.Content{MessageID: "msg_update", Phase: provider.MessagePhaseCommentary, Text: "Checking."}),
			content(call), content(provider.Content{MessageID: "msg_final", Phase: provider.MessagePhaseFinalAnswer, Text: "Done."}),
			{Status: provider.CompletionStatusCompleted},
		}, nil, "completed", []string{"message", "function_call", "message"}},
		{"completed tool before cutoff", []provider.Completion{content(call), {Status: provider.CompletionStatusIncomplete}}, nil, "incomplete", []string{"function_call"}},
		{"provider reported failure", []provider.Completion{
			content(provider.Content{MessageID: "msg_update", Text: "Checking."}), content(call),
			content(provider.Content{MessageID: "msg_partial", Text: "Partial result"}),
			{Status: provider.CompletionStatusFailed, StopDetails: &provider.StopDetails{Type: "provider_failure"}},
		}, nil, "failed", []string{"message", "function_call"}},
		{"transport failed during arguments", []provider.Completion{
			content(provider.Content{MessageID: "msg_update", Text: "Checking."}),
			content(provider.ToolCallContent(provider.ToolCall{ID: "call_check", Name: "check", Arguments: `{"path":`})),
		}, errors.New("connection reset"), "failed", []string{"message", "function_call"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := append([]provider.Completion{{Model: "backend-model"}}, tc.chunks...)
			chunks = append(chunks, provider.Completion{Usage: &provider.Usage{InputTokens: 10, OutputTokens: 5}})
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter("alias", responsesContractCompleter{chunks: chunks, err: tc.err})
			rec := httptest.NewRecorder()
			New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"alias","input":"check","stream":true,"include":["reasoning.encrypted_content"]}`)))
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
			}
			events, err := harness.ParseSSE(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			added, done := map[int]map[string]any{}, map[int]map[string]any{}
			sequence, terminals := -1, 0
			var final map[string]any
			for _, event := range events {
				if terminals != 0 {
					t.Fatal("event emitted after the terminal response")
				}
				data, _ := json.Marshal(event.Data)
				var typed sdk.ResponseStreamEventUnion
				if err := json.Unmarshal(data, &typed); err != nil {
					t.Fatalf("OpenAI SDK rejected %s: %v", event.Event, err)
				}
				next := int(event.Data["sequence_number"].(float64))
				if next <= sequence {
					t.Fatalf("sequence number %d after %d", next, sequence)
				}
				sequence = next
				switch event.Event {
				case "response.output_item.added":
					index := int(event.Data["output_index"].(float64))
					if index != len(added) {
						t.Fatalf("output index %d, want %d", index, len(added))
					}
					added[index] = event.Data["item"].(map[string]any)
				case "response.output_item.done":
					index := int(event.Data["output_index"].(float64))
					item := event.Data["item"].(map[string]any)
					if added[index] == nil || done[index] != nil || added[index]["id"] != item["id"] {
						t.Fatalf("invalid item lifecycle: %s", data)
					}
					done[index] = item
				case "response.completed", "response.incomplete", "response.failed":
					terminals++
					final = event.Data["response"].(map[string]any)
					if event.Event != "response."+tc.status || final["status"] != tc.status {
						t.Fatalf("terminal response=%s, want %s", data, tc.status)
					}
				default:
					if strings.HasSuffix(event.Event, ".delta") {
						index := int(event.Data["output_index"].(float64))
						if added[index] == nil || done[index] != nil || added[index]["id"] != event.Data["item_id"] {
							t.Fatalf("delta outside item lifecycle: %s", data)
						}
					}
				}
			}
			if terminals != 1 {
				t.Fatalf("terminal responses=%d", terminals)
			}
			output := final["output"].([]any)
			var types []string
			for i, raw := range output {
				item := raw.(map[string]any)
				types = append(types, item["type"].(string))
				if added[i] == nil || item["id"] != added[i]["id"] {
					t.Fatalf("snapshot contains an unannounced item: %v", item)
				}
				if done[i] != nil && !reflect.DeepEqual(done[i], item) {
					t.Fatalf("final snapshot changed completed item:\ndone=%v\nfinal=%v", done[i], item)
				}
				if status := item["status"]; status != "completed" && status != "incomplete" {
					t.Fatalf("invalid terminal item status: %v", item)
				}
			}
			if !reflect.DeepEqual(types, tc.types) || len(output) != len(added) {
				t.Fatalf("output=%v, want %v", types, tc.types)
			}
			usage := final["usage"].(map[string]any)
			if final["model"] != "backend-model" || usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(5) {
				t.Fatalf("terminal metadata lost: %v", final)
			}
			if tc.status == "failed" && final["error"] == nil {
				t.Fatal("failed response omitted its error")
			}
		})
	}
}
