package responses

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestResponseUsageIncludesCachedTokens(t *testing.T) {
	usage := responseUsage(&provider.Usage{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 80,
	})

	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.InputTokensDetails == nil {
		t.Fatal("expected input token details")
	}
	if usage.InputTokensDetails.CachedTokens != 80 {
		t.Fatalf("expected cached tokens 80, got %d", usage.InputTokensDetails.CachedTokens)
	}
	if usage.TotalTokens != 120 {
		t.Fatalf("expected total tokens 120, got %d", usage.TotalTokens)
	}
}

func TestReasoningRequestedByEncryptedContentInclude(t *testing.T) {
	req := ResponsesRequest{
		Include: []string{"reasoning.encrypted_content"},
	}

	if !reasoningRequested(req) {
		t.Fatal("expected encrypted_content include to request reasoning output")
	}
}

func TestResponseOutputsReasoningKeepsEmptySummaryArray(t *testing.T) {
	outputs := responseOutputs(&provider.Message{
		Content: []provider.Content{
			provider.ReasoningContent(provider.Reasoning{
				ID:        "rs_1",
				Signature: "ENC_1",
			}),
		},
	}, "msg_1", "completed", responseOutputOptions{IncludeReasoning: true})

	if len(outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(outputs))
	}

	data, err := json.Marshal(outputs[0])
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	summary, ok := got["summary"].([]any)
	if !ok {
		t.Fatalf("expected summary array in %s", string(data))
	}
	if len(summary) != 0 {
		t.Fatalf("expected empty summary array, got %v", summary)
	}
	if got["encrypted_content"] != "ENC_1" {
		t.Fatalf("expected encrypted_content ENC_1, got %v", got["encrypted_content"])
	}
}

func TestFunctionCallOutputAcceptsStringAndArray(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "string",
			payload: `[{"type":"function_call_output","call_id":"c1","output":"hello"}]`,
			want:    "hello",
		},
		{
			name:    "array of output_text parts",
			payload: `[{"type":"function_call_output","call_id":"c1","output":[{"type":"output_text","text":"hello"},{"type":"output_text","text":" world"}]}]`,
			want:    "hello world",
		},
		{
			name:    "empty array",
			payload: `[{"type":"function_call_output","call_id":"c1","output":[]}]`,
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input ResponsesInput
			if err := json.Unmarshal([]byte(tc.payload), &input); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(input.Items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(input.Items))
			}
			item := input.Items[0]
			if item.Type != InputItemTypeFunctionCallOutput {
				t.Fatalf("expected type function_call_output, got %s", item.Type)
			}
			if item.InputFunctionCallOutput == nil {
				t.Fatal("expected InputFunctionCallOutput to be set")
			}
			if item.InputFunctionCallOutput.CallID != "c1" {
				t.Fatalf("expected call_id c1, got %q", item.InputFunctionCallOutput.CallID)
			}
			if item.InputFunctionCallOutput.Output != tc.want {
				t.Fatalf("expected output %q, got %q", tc.want, item.InputFunctionCallOutput.Output)
			}
		})
	}
}

func TestApplyPatchCallOutputAcceptsArray(t *testing.T) {
	payload := `[{"type":"apply_patch_call_output","call_id":"c2","status":"completed","output":[{"type":"output_text","text":"ok"}]}]`

	var input ResponsesInput
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(input.Items) != 1 || input.Items[0].InputApplyPatchCallOutput == nil {
		t.Fatalf("expected one apply_patch_call_output item, got %+v", input.Items)
	}
	got := input.Items[0].InputApplyPatchCallOutput
	if got.CallID != "c2" || got.Status != "completed" || got.Output != "ok" {
		t.Fatalf("unexpected item: %+v", got)
	}
}

func TestResponseOutputsPreservesCompactionOrder(t *testing.T) {
	outputs := responseOutputs(&provider.Message{
		Content: []provider.Content{
			provider.CompactionContent(provider.Compaction{ID: "cmp_1", Signature: "ENC_1"}),
			provider.TextContent("4"),
			provider.CompactionContent(provider.Compaction{ID: "cmp_2", Signature: "ENC_2"}),
		},
	}, "msg_1", "completed", responseOutputOptions{})

	if len(outputs) != 3 {
		t.Fatalf("expected 3 outputs, got %d", len(outputs))
	}

	if outputs[0].Type != ResponseOutputTypeCompaction || outputs[0].CompactionOutputItem.ID != "cmp_1" {
		t.Fatalf("output 0: expected cmp_1, got %+v", outputs[0])
	}
	if outputs[1].Type != ResponseOutputTypeMessage || outputs[1].OutputMessage.Contents[0].Text != "4" {
		t.Fatalf("output 1: expected message text, got %+v", outputs[1])
	}
	if outputs[2].Type != ResponseOutputTypeCompaction || outputs[2].CompactionOutputItem.ID != "cmp_2" {
		t.Fatalf("output 2: expected cmp_2, got %+v", outputs[2])
	}
}
