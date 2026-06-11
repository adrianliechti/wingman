package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// TestToMessage_ThinkingBlocks verifies that thinking and redacted_thinking
// content blocks on input are preserved as Reasoning content. Without this
// the signature is dropped on round-trip, causing Anthropic to reject the
// next turn when thinking is enabled.
func TestToMessage_ThinkingBlocks(t *testing.T) {
	body := []byte(`[
		{"type":"thinking","thinking":"thinking step by step","signature":"SIG_ABC"},
		{"type":"redacted_thinking","data":"REDACTED_BLOB"},
		{"type":"text","text":"final answer"}
	]`)

	var blocks []ContentBlockParam
	if err := json.Unmarshal(body, &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	msg, err := toMessage(0, MessageParam{Role: MessageRoleAssistant, Content: blocksToAny(blocks)})
	if err != nil {
		t.Fatalf("toMessage: %v", err)
	}

	var reasonings []provider.Reasoning
	var hasText bool
	for _, c := range msg.Content {
		if c.Reasoning != nil {
			reasonings = append(reasonings, *c.Reasoning)
		}
		if c.Text != "" {
			hasText = true
		}
	}

	if len(reasonings) != 2 {
		t.Fatalf("expected 2 reasoning entries, got %d: %+v", len(reasonings), reasonings)
	}

	if reasonings[0].Text != "thinking step by step" || reasonings[0].Signature != "SIG_ABC" {
		t.Errorf("thinking block: got (text=%q, sig=%q), want (\"thinking step by step\", \"SIG_ABC\")",
			reasonings[0].Text, reasonings[0].Signature)
	}

	// redacted_thinking carries only the opaque data blob.
	if reasonings[1].Text != "" || reasonings[1].Signature != "REDACTED_BLOB" || !reasonings[1].Redacted {
		t.Errorf("redacted_thinking block: got (text=%q, sig=%q, redacted=%v), want (\"\", \"REDACTED_BLOB\", true)",
			reasonings[1].Text, reasonings[1].Signature, reasonings[1].Redacted)
	}

	if reasonings[0].Redacted {
		t.Error("thinking block should not be marked redacted")
	}

	if !hasText {
		t.Errorf("expected text content to also be present, got: %+v", msg.Content)
	}
}

// TestToMessage_DocumentBlocks verifies that document content blocks flow
// through correctly. Base64 (PDF) sources become File content; URL sources
// are fetched (no provider consumes raw URLs); plain-text sources inline as
// text so they work across providers without a dedicated document concept.
func TestToMessage_DocumentBlocks(t *testing.T) {
	pdfBytes := []byte("%PDF-1.4 fake pdf bytes")
	pdfB64 := base64.StdEncoding.EncodeToString(pdfBytes)

	urlBytes := []byte("%PDF-1.4 fetched pdf bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(urlBytes)
	}))
	defer server.Close()

	body := []byte(`[
		{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"` + pdfB64 + `"}},
		{"type":"document","source":{"type":"text","media_type":"text/plain","data":"hello world"}},
		{"type":"document","source":{"type":"url","url":"` + server.URL + `/doc.pdf"}},
		{"type":"text","text":"summarize"}
	]`)

	var blocks []ContentBlockParam
	if err := json.Unmarshal(body, &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	msg, err := toMessage(0, MessageParam{Role: MessageRoleUser, Content: blocksToAny(blocks)})
	if err != nil {
		t.Fatalf("toMessage: %v", err)
	}

	var files []*provider.File
	var texts []string
	for _, c := range msg.Content {
		if c.File != nil {
			files = append(files, c.File)
		}
		if c.Text != "" {
			texts = append(texts, c.Text)
		}
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 file entries (base64 PDF + URL), got %d", len(files))
	}

	if string(files[0].Content) != string(pdfBytes) || files[0].ContentType != "application/pdf" {
		t.Errorf("base64 PDF: bytes or content-type mismatch (got type=%q, len=%d)",
			files[0].ContentType, len(files[0].Content))
	}
	if string(files[1].Content) != string(urlBytes) || files[1].ContentType != "application/pdf" {
		t.Errorf("url doc: got type=%q content=%q", files[1].ContentType, string(files[1].Content))
	}

	// "hello world" text doc + the trailing "summarize" text block.
	if len(texts) != 2 || texts[0] != "hello world" || texts[1] != "summarize" {
		t.Errorf("expected texts [hello world, summarize], got %v", texts)
	}
}

// TestToMessage_ImageURLBlock verifies that URL-sourced images are fetched
// and carry the media type from the response when the block omits it.
func TestToMessage_ImageURLBlock(t *testing.T) {
	imgBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(imgBytes)
	}))
	defer server.Close()

	body := []byte(`[
		{"type":"image","source":{"type":"url","url":"` + server.URL + `/cat.png"}},
		{"type":"text","text":"describe"}
	]`)

	var blocks []ContentBlockParam
	if err := json.Unmarshal(body, &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	msg, err := toMessage(0, MessageParam{Role: MessageRoleUser, Content: blocksToAny(blocks)})
	if err != nil {
		t.Fatalf("toMessage: %v", err)
	}

	var file *provider.File
	for _, c := range msg.Content {
		if c.File != nil {
			file = c.File
		}
	}

	if file == nil {
		t.Fatal("expected file content")
	}
	if string(file.Content) != string(imgBytes) {
		t.Errorf("image bytes mismatch (len=%d)", len(file.Content))
	}
	if file.ContentType != "image/png" {
		t.Errorf("content type: got %q, want image/png", file.ContentType)
	}
}

// TestToMessage_SystemRole verifies that a "system"-role input message (now
// allowed mid-conversation by newer Claude models) maps to a provider system
// message instead of being rejected as an unknown role.
func TestToMessage_SystemRole(t *testing.T) {
	msg, err := toMessage(0, MessageParam{Role: MessageRoleSystem, Content: "be terse"})
	if err != nil {
		t.Fatalf("toMessage: %v", err)
	}

	if msg.Role != provider.MessageRoleSystem {
		t.Errorf("role: got %q, want %q", msg.Role, provider.MessageRoleSystem)
	}

	if len(msg.Content) != 1 || msg.Content[0].Text != "be terse" {
		t.Errorf("content: got %+v, want single text %q", msg.Content, "be terse")
	}
}

// TestToMessages_SystemRoleAfterTopLevel verifies ordering: the top-level
// system prompt is prepended, and a mid-conversation system message keeps its
// position in the resulting message list.
func TestToMessages_SystemRoleAfterTopLevel(t *testing.T) {
	messages, err := toMessages("top-level system", []MessageParam{
		{Role: MessageRoleUser, Content: "hi"},
		{Role: MessageRoleSystem, Content: "now switch to formal tone"},
		{Role: MessageRoleAssistant, Content: "Understood."},
	})
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}

	wantRoles := []provider.MessageRole{
		provider.MessageRoleSystem,
		provider.MessageRoleUser,
		provider.MessageRoleSystem,
		provider.MessageRoleAssistant,
	}

	if len(messages) != len(wantRoles) {
		t.Fatalf("expected %d messages, got %d", len(wantRoles), len(messages))
	}

	for i, want := range wantRoles {
		if messages[i].Role != want {
			t.Errorf("messages[%d].Role: got %q, want %q", i, messages[i].Role, want)
		}
	}
}

func blocksToAny(blocks []ContentBlockParam) any {
	out := make([]any, len(blocks))
	for i, b := range blocks {
		raw, _ := json.Marshal(b)
		var v any
		_ = json.Unmarshal(raw, &v)
		out[i] = v
	}
	return out
}

func TestToMessage_ServerToolUseRoundTripsAsText(t *testing.T) {
	body := []byte(`[
		{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"go 1.24 release"}},
		{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[
			{"type":"web_search_result","url":"https://go.dev/blog/go1.24","title":"Go 1.24","encrypted_content":"x"}
		]},
		{"type":"server_tool_use","id":"srvtoolu_2","name":"web_fetch","input":{"url":"https://go.dev/blog/go1.24"}},
		{"type":"web_fetch_tool_result","tool_use_id":"srvtoolu_2","content":{"url":"https://go.dev/blog/go1.24","retrieved_at":"2025-05-30T00:00:00Z"}},
		{"type":"text","text":"final answer"}
	]`)

	var blocks []ContentBlockParam
	if err := json.Unmarshal(body, &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	msg, err := toMessage(0, MessageParam{Role: MessageRoleAssistant, Content: blocksToAny(blocks)})
	if err != nil {
		t.Fatalf("toMessage: %v", err)
	}

	if len(msg.Content) != 5 {
		t.Fatalf("expected 5 content blocks, got %d", len(msg.Content))
	}

	want := []string{
		`[web_search: "go 1.24 release"]`,
		`[web_search_result: Go 1.24 (https://go.dev/blog/go1.24)]`,
		`[web_fetch: https://go.dev/blog/go1.24]`,
		`[web_fetch_result: https://go.dev/blog/go1.24]`,
		"final answer",
	}
	for i, w := range want {
		if msg.Content[i].Text != w {
			t.Errorf("content[%d].Text = %q, want %q", i, msg.Content[i].Text, w)
		}
	}
}

func TestToTools_RejectsWebSearch(t *testing.T) {
	in := []ToolParam{
		{Type: "custom", Name: "get_weather", InputSchema: map[string]any{"type": "object"}},
		{Type: "web_search_20250305", Name: "web_search"},
	}

	_, err := toTools(in)
	if err == nil {
		t.Fatal("expected error for web_search_20250305")
	}
	if msg := err.Error(); !strings.Contains(msg, "tools.1") || !strings.Contains(msg, "web_search_20250305") {
		t.Errorf("error = %q", msg)
	}
}

func TestToTools_RejectsWebFetch(t *testing.T) {
	in := []ToolParam{
		{Type: "web_fetch_20250910", Name: "web_fetch"},
	}

	_, err := toTools(in)
	if err == nil {
		t.Fatal("expected error for web_fetch_20250910")
	}
	if msg := err.Error(); !strings.Contains(msg, "tools.0") || !strings.Contains(msg, "web_fetch_20250910") {
		t.Errorf("error = %q", msg)
	}
}

func TestToTools_PassesThroughRegular(t *testing.T) {
	in := []ToolParam{
		{Type: "custom", Name: "get_weather", InputSchema: map[string]any{"type": "object"}},
	}

	tools, err := toTools(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools length = %d", len(tools))
	}
}

// TestToContentBlocks_RedactedThinking verifies redacted reasoning round-trips
// as a redacted_thinking block with the opaque blob in `data`.
func TestToContentBlocks_RedactedThinking(t *testing.T) {
	content := []provider.Content{
		provider.ReasoningContent(provider.Reasoning{Text: "step", Signature: "SIG"}),
		provider.ReasoningContent(provider.Reasoning{Signature: "BLOB", Redacted: true}),
		provider.TextContent("answer"),
	}

	blocks := toContentBlocks(content, true)

	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d: %+v", len(blocks), blocks)
	}

	if blocks[0].Type != "thinking" || blocks[0].Thinking != "step" || blocks[0].Signature != "SIG" {
		t.Errorf("thinking block: %+v", blocks[0])
	}
	if blocks[1].Type != "redacted_thinking" || blocks[1].Data != "BLOB" || blocks[1].Signature != "" {
		t.Errorf("redacted_thinking block: %+v", blocks[1])
	}
	if blocks[2].Type != "text" || blocks[2].Text == nil || *blocks[2].Text != "answer" {
		t.Errorf("text block: %+v", blocks[2])
	}
}
