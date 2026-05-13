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
		name      string
		payload   string
		wantTexts []string
	}{
		{
			name:      "string normalized to single output_text",
			payload:   `[{"type":"function_call_output","call_id":"c1","output":"hello"}]`,
			wantTexts: []string{"hello"},
		},
		{
			name:      "array of output_text parts",
			payload:   `[{"type":"function_call_output","call_id":"c1","output":[{"type":"output_text","text":"hello"},{"type":"output_text","text":" world"}]}]`,
			wantTexts: []string{"hello", " world"},
		},
		{
			name:      "empty array",
			payload:   `[{"type":"function_call_output","call_id":"c1","output":[]}]`,
			wantTexts: nil,
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
			fco := input.Items[0].InputFunctionCallOutput
			if fco == nil || fco.CallID != "c1" {
				t.Fatalf("unexpected item: %+v", input.Items[0])
			}
			if len(fco.Output) != len(tc.wantTexts) {
				t.Fatalf("expected %d parts, got %d", len(tc.wantTexts), len(fco.Output))
			}
			for i, want := range tc.wantTexts {
				if fco.Output[i].Text != want {
					t.Fatalf("part %d: expected %q, got %q", i, want, fco.Output[i].Text)
				}
			}
		})
	}
}

func TestFunctionCallOutputPreservesImagePart(t *testing.T) {
	// 1x1 transparent PNG
	pngDataURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

	payload := `[{"type":"function_call_output","call_id":"c1","output":[` +
		`{"type":"output_text","text":"see image"},` +
		`{"type":"input_image","image_url":"` + pngDataURL + `"}` +
		`]}]`

	var input ResponsesInput
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	messages, err := toMessages(input.Items, "")
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("expected 1 message with 1 content, got %+v", messages)
	}

	tr := messages[0].Content[0].ToolResult
	if tr == nil || tr.ID != "c1" {
		t.Fatalf("expected ToolResult c1, got %+v", messages[0].Content[0])
	}
	if len(tr.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(tr.Parts), tr.Parts)
	}
	if tr.Parts[0].Text != "see image" {
		t.Fatalf("part 0: expected text 'see image', got %q", tr.Parts[0].Text)
	}
	if tr.Parts[1].File == nil || tr.Parts[1].File.ContentType != "image/png" {
		t.Fatalf("part 1: expected image/png file, got %+v", tr.Parts[1])
	}
}

func TestComputerCallOutputExtractsScreenshot(t *testing.T) {
	pngDataURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

	payload := `[{"type":"computer_call_output","call_id":"c3","output":{"type":"computer_screenshot","image_url":"` + pngDataURL + `"}}]`

	var input ResponsesInput
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	messages, err := toMessages(input.Items, "")
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("expected 1 message with 1 content, got %+v", messages)
	}

	tr := messages[0].Content[0].ToolResult
	if tr == nil || tr.ID != "c3" {
		t.Fatalf("expected ToolResult c3, got %+v", messages[0].Content[0])
	}
	if len(tr.Parts) != 1 || tr.Parts[0].File == nil {
		t.Fatalf("expected one file part, got %+v", tr.Parts)
	}
	if tr.Parts[0].File.ContentType != "image/png" {
		t.Fatalf("expected image/png, got %q", tr.Parts[0].File.ContentType)
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
	if got.CallID != "c2" || got.Status != "completed" {
		t.Fatalf("unexpected item fields: %+v", got)
	}
	if len(got.Output) != 1 || got.Output[0].Text != "ok" {
		t.Fatalf("expected one output_text part, got %+v", got.Output)
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
