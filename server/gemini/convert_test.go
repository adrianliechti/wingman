package gemini

import (
	"encoding/json/v2"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
)

func TestUsageMetadataPreservesReasoningPresence(t *testing.T) {
	for _, count := range []*int{nil, new(0), new(3)} {
		metadata := toUsageMetadata(&provider.Usage{OutputTokens: 5, ReasoningTokens: count})
		data, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		value, reported := wire["thoughtsTokenCount"]
		if reported != (count != nil) || reported && value != float64(*count) {
			t.Fatalf("wire usage = %s, want reasoning count %v", data, count)
		}
	}
}

func TestUsageMetadataIncludesCachedTokens(t *testing.T) {
	metadata := toUsageMetadata(&provider.Usage{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 80,
	})

	if metadata == nil {
		t.Fatal("expected usage metadata")
	}
	if metadata.CachedContentTokenCount != 80 {
		t.Fatalf("expected cached content token count 80, got %d", metadata.CachedContentTokenCount)
	}
	if metadata.TotalTokenCount != 120 {
		t.Fatalf("expected total token count 120, got %d", metadata.TotalTokenCount)
	}
}

// TestUsageMetadataSplitsReasoningFromCandidates verifies that the intermediate
// reasoning-inclusive OutputTokens is split back into Gemini's wire shape, where
// CandidatesTokenCount excludes thinking and ThoughtsTokenCount carries it.
func TestUsageMetadataSplitsReasoningFromCandidates(t *testing.T) {
	metadata := toUsageMetadata(&provider.Usage{
		InputTokens:     100,
		OutputTokens:    20, // 14 visible + 6 thinking
		ReasoningTokens: new(6),
	})

	if metadata == nil {
		t.Fatal("expected usage metadata")
	}
	if metadata.CandidatesTokenCount != 14 {
		t.Fatalf("expected candidates token count 14, got %d", metadata.CandidatesTokenCount)
	}
	if metadata.ThoughtsTokenCount == nil {
		t.Fatal("expected thoughts token count")
	}
	if *metadata.ThoughtsTokenCount != 6 {
		t.Fatalf("expected thoughts token count 6, got %d", *metadata.ThoughtsTokenCount)
	}
	if metadata.TotalTokenCount != 120 {
		t.Fatalf("expected total token count 120, got %d", metadata.TotalTokenCount)
	}
}

func TestNormalizeResponseSchema(t *testing.T) {
	schema := map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"title":  map[string]any{"type": "STRING"},
			"series": map[string]any{"type": "STRING", "nullable": true},
			"tags": map[string]any{"type": "ARRAY", "minItems": "1", "maxItems": "2",
				"items": map[string]any{"type": "STRING", "minLength": "0", "maxLength": "9007199254740993"}},
		},
		"minProperties":        "1",
		"defs":                 map[string]any{"Label": map[string]any{"type": "STRING"}},
		"additionalProperties": map[string]any{"ref": "#/defs/Label"},
		"maxProperties":        "3",
		"required":             []string{"title"},
		"propertyOrdering":     []string{"title", "series", "tags"},
	}

	got := normalizeResponseSchema(schema)

	defs, ok := got["$defs"].(map[string]any)
	if !ok || defs["Label"].(map[string]any)["type"] != "string" || got["additionalProperties"].(map[string]any)["$ref"] != "#/$defs/Label" {
		t.Fatalf("invalid JSON Schema definition or reference: %v", got)
	}

	if got["type"] != "object" {
		t.Errorf("type: %v", got["type"])
	}

	if _, ok := got["propertyOrdering"]; ok {
		t.Error("propertyOrdering should be stripped")
	}

	props := got["properties"].(map[string]any)

	if props["title"].(map[string]any)["type"] != "string" {
		t.Errorf("title type: %v", props["title"])
	}

	series := props["series"].(map[string]any)
	if _, ok := series["nullable"]; ok {
		t.Error("nullable should be folded into type")
	}
	if types, _ := series["type"].([]any); len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Errorf("series type: %v", series["type"])
	}

	tags := props["tags"].(map[string]any)
	items := tags["items"].(map[string]any)
	if items["type"] != "string" {
		t.Errorf("items type: %v", items["type"])
	}
	if tags["minItems"] != int64(1) || tags["maxItems"] != int64(2) ||
		items["minLength"] != int64(0) || items["maxLength"] != int64(9007199254740993) ||
		got["minProperties"] != int64(1) || got["maxProperties"] != int64(3) {
		t.Fatalf("string-encoded size limits lost their integer values: %v", got)
	}

	// input untouched
	if schema["type"] != "OBJECT" {
		t.Error("input schema was mutated")
	}
}

// A thought signature that precedes a function call must survive a Gemini
// round trip on that call, in the same order, so the next request replays the
// thought step before the call as the backend produced it.
func TestFunctionCallThoughtSignatureSurvivesReplay(t *testing.T) {
	signature := google.EncodeThoughtSignature([]byte{0xff, 0x00, 0x80})
	content := toCandidate([]provider.Content{
		provider.TextContent("Checking"),
		provider.ReasoningContent(provider.Reasoning{Signature: signature}),
		provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`}),
	}, FinishReasonStop).Content
	if content == nil || len(content.Parts) != 2 {
		t.Fatalf("parts = %+v", content)
	}
	if len(content.Parts[0].ThoughtSignature) != 0 || content.Parts[1].FunctionCall == nil || google.EncodeThoughtSignature(content.Parts[1].ThoughtSignature) != signature {
		t.Fatalf("signature not on the function call part: %+v %+v", content.Parts[0], content.Parts[1])
	}

	wire, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	var replayed Content
	if err := json.Unmarshal(wire, &replayed); err != nil {
		t.Fatal(err)
	}
	messages, err := toMessages(nil, []*Content{{Role: "user", Parts: []*Part{{Text: "Hi"}}}, &replayed})
	if err != nil {
		t.Fatal(err)
	}
	got := messages[len(messages)-1].Content
	if len(got) != 3 || got[0].Text != "Checking" || got[1].Reasoning == nil || got[1].Reasoning.Signature != signature || got[2].ToolCall == nil || got[2].ToolCall.ID != "call_1" {
		t.Fatalf("replayed content = %+v", got)
	}
}

// Gemini parts have no reasoning item field. Preserve the original item ID
// inside the thought signature for replay.
func TestReasoningIDSurvivesReplay(t *testing.T) {
	for _, signature := range []string{"@gpt:encrypted", google.EncodeThoughtSignature([]byte{0x40, 0x01, 0x3a, 0xff})} {
		content := toCandidate([]provider.Content{
			provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: provider.TextParts("Checking"), Signature: signature}),
			provider.TextContent("Done"),
			provider.ReasoningContent(provider.Reasoning{ID: "rs_2", Signature: signature}),
			provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "lookup", Arguments: `{}`}),
			provider.ReasoningContent(provider.Reasoning{Signature: signature}),
			provider.TextContent("Unidentified"),
		}, FinishReasonStop).Content
		wire, err := json.Marshal(content)
		if err != nil {
			t.Fatal(err)
		}
		var replayed Content
		if err := json.Unmarshal(wire, &replayed); err != nil {
			t.Fatal(err)
		}
		messages, err := toMessages(nil, []*Content{&replayed})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range messages[0].Content {
			if r := c.Reasoning; r != nil {
				got = append(got, r.ID+"="+r.Signature)
			}
		}
		if want := []string{"rs_1=" + signature, "rs_2=" + signature, "=" + signature}; !slices.Equal(got, want) {
			t.Fatalf("replayed reasoning %v, want %v", got, want)
		}
	}
}

// python-genai's types.Schema serializes OpenAPI types in upper case. Other
// backends require JSON Schema; parametersJsonSchema is passed through.
func TestFunctionParametersBecomeJSONSchema(t *testing.T) {
	tools := toTools([]*Tool{{FunctionDeclarations: []*FunctionDeclaration{
		{Name: "openapi", Parameters: map[string]any{"type": "OBJECT", "properties": map[string]any{
			"city": map[string]any{"type": "STRING", "nullable": true},
		}, "required": []any{"city"}, "propertyOrdering": []any{"city"}}},
		{Name: "json", ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{"STRING": map[string]any{"const": "OBJECT"}}}},
	}}}, false)
	city := tools[0].Parameters["properties"].(map[string]any)["city"].(map[string]any)
	if tools[0].Parameters["type"] != "object" || !slices.Equal(city["type"].([]any), []any{"string", "null"}) || tools[0].Parameters["propertyOrdering"] != nil {
		t.Fatalf("OpenAPI parameters were not converted: %v", tools[0].Parameters)
	}
	if property := tools[1].Parameters["properties"].(map[string]any)["STRING"].(map[string]any); property["const"] != "OBJECT" {
		t.Fatalf("parametersJsonSchema changed: %v", tools[1].Parameters)
	}
}

// Gemini clients send the signature on the functionCall part, as the native
// API returns it.
func TestClientFunctionCallSignatureIsKept(t *testing.T) {
	messages, err := toMessages(nil, []*Content{
		{Role: "user", Parts: []*Part{{Text: "Hi"}}},
		{Role: "model", Parts: []*Part{{FunctionCall: &FunctionCall{ID: "call_1", Name: "lookup", Args: map[string]any{}}, ThoughtSignature: []byte("SIG")}}},
		{Role: "user", Parts: []*Part{{FunctionResponse: &FunctionResponse{ID: "call_1", Name: "lookup", Response: map[string]any{"ok": true}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := messages[1].Content
	if len(got) != 2 || got[0].Reasoning == nil || got[0].Reasoning.Signature != google.EncodeThoughtSignature([]byte("SIG")) || got[1].ToolCall == nil {
		t.Fatalf("model content = %+v", got)
	}
}

func TestNativeTextSignatureStaysOnItsPart(t *testing.T) {
	want := google.EncodeThoughtSignature([]byte{0xff, 0x00, 0x80})
	message, err := toMessage(Content{Role: "model", Parts: []*Part{
		{Text: "First"},
		{Text: "Second", ThoughtSignature: google.DecodeThoughtSignature(want)},
	}}, map[string][]string{})
	if err != nil {
		t.Fatal(err)
	}
	reply := toCandidate(message.Content, FinishReasonStop).Content
	if len(reply.Parts) != 2 || len(reply.Parts[0].ThoughtSignature) != 0 || google.EncodeThoughtSignature(reply.Parts[1].ThoughtSignature) != want {
		t.Fatalf("signature moved to another text part: %+v", reply)
	}
}

func TestBinaryThoughtSignatureWithAtAndColonIsNotParsedAsRealm(t *testing.T) {
	// Protobuf wire bytes starting with '@' (0x40 = field 8 varint) and containing
	// ':' (0x3a = field 7 length-delimited) plus binary control/high bytes.
	rawSig := []byte{0x40, 0x01, 0x3a, 0x80, 0xff, 0x00}
	want := google.EncodeThoughtSignature(rawSig)

	messages, err := toMessages(nil, []*Content{
		{Role: "user", Parts: []*Part{{Text: "Hi"}}},
		{Role: "model", Parts: []*Part{{FunctionCall: &FunctionCall{ID: "call_1", Name: "lookup", Args: map[string]any{}}, ThoughtSignature: rawSig}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := messages[1].Content
	if len(got) != 2 || got[0].Reasoning == nil || got[0].Reasoning.Signature != want {
		t.Fatalf("binary signature was corrupted as realm-tagged: got %+v, want %q", got, want)
	}
}

func TestFinishReasonAndRefusalFromCompletion(t *testing.T) {
	if got := toFinishReason(&provider.Completion{StopReason: provider.StopReasonMaxTokens}); got != FinishReasonMaxTokens {
		t.Fatalf("StopReasonMaxTokens -> %q, want %q", got, FinishReasonMaxTokens)
	}
	if got := toFinishReason(&provider.Completion{StopReason: provider.StopReasonRefusal}); got != FinishReasonSafety {
		t.Fatalf("StopReasonRefusal -> %q, want %q", got, FinishReasonSafety)
	}

	content := toCandidate([]provider.Content{provider.RefusalContent("cannot comply")}, FinishReasonSafety).Content
	if content == nil || len(content.Parts) != 1 || content.Parts[0].Text != "cannot comply" {
		t.Fatalf("refusal content = %+v", content)
	}
}

func TestToMessagesRejectsInvalidRoleAndSkipsNilElements(t *testing.T) {
	if _, err := toMessages(nil, []*Content{{Role: "system", Parts: []*Part{{Text: "hi"}}}}); err == nil {
		t.Fatal("expected error for invalid role 'system'")
	}

	msgs, err := toMessages(&Content{Parts: []*Part{nil, {Text: "sys"}}}, []*Content{nil, {Role: "user", Parts: []*Part{nil, {Text: "hello"}}}})
	if err != nil || len(msgs) != 2 {
		t.Fatalf("toMessages with nil elements: len=%d, err=%v", len(msgs), err)
	}

	tools := toTools([]*Tool{nil, {FunctionDeclarations: []*FunctionDeclaration{nil, {Name: "fn"}}}}, false)
	if len(tools) != 1 || tools[0].Name != "fn" {
		t.Fatalf("toTools with nil elements: %+v", tools)
	}
}

func TestToMessagesFunctionResponseForcesUserRole(t *testing.T) {
	messages, err := toMessages(nil, []*Content{
		{Role: "user", Parts: []*Part{{Text: "Hi"}}},
		{Role: "model", Parts: []*Part{{FunctionCall: &FunctionCall{ID: "toolu_1", Name: "lookup", Args: map[string]any{"q": "x"}}}}},
		{Role: "model", Parts: []*Part{{FunctionResponse: &FunctionResponse{ID: "toolu_1", Name: "lookup", Response: map[string]any{"ok": true}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	if messages[2].Role != provider.MessageRoleUser {
		t.Fatalf("messages[2].Role = %q, want %q", messages[2].Role, provider.MessageRoleUser)
	}
	if result, ok := messages[2].ToolResult(); !ok || result.ID != "toolu_1" {
		t.Fatalf("messages[2] tool result = %+v, %v", result, ok)
	}
}

func TestMixedFunctionResponseIDs(t *testing.T) {
	history := []*Content{
		{Role: "model", Parts: []*Part{
			{FunctionCall: &FunctionCall{Name: "lookup", ID: "call1"}},
			{FunctionCall: &FunctionCall{Name: "lookup", ID: "call2"}},
			{FunctionCall: &FunctionCall{Name: "lookup", ID: "call3"}},
		}},
		{Role: "user", Parts: []*Part{
			{FunctionResponse: &FunctionResponse{Name: "lookup", ID: "call2"}},
			{FunctionResponse: &FunctionResponse{Name: "lookup"}},
			{FunctionResponse: &FunctionResponse{Name: "lookup"}},
		}},
	}
	messages, err := toMessages(nil, history)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"call2", "call1", "call3"} {
		if got := messages[1].Content[i].ToolResult.ID; got != want {
			t.Fatalf("result %d paired with %q, want %q", i, got, want)
		}
	}
}
