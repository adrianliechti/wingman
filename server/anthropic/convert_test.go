package anthropic

import (
	"encoding/base64"
	"encoding/json"
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

	msg, err := toMessage(MessageParam{Role: MessageRoleAssistant, Content: blocksToAny(blocks)})
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
	if reasonings[1].Text != "" || reasonings[1].Signature != "REDACTED_BLOB" {
		t.Errorf("redacted_thinking block: got (text=%q, sig=%q), want (\"\", \"REDACTED_BLOB\")",
			reasonings[1].Text, reasonings[1].Signature)
	}

	if !hasText {
		t.Errorf("expected text content to also be present, got: %+v", msg.Content)
	}
}

// TestToMessage_DocumentBlocks verifies that document content blocks flow
// through correctly. Base64 (PDF) and URL sources become File content;
// plain-text sources inline as text so they work across providers without
// a dedicated document concept.
func TestToMessage_DocumentBlocks(t *testing.T) {
	pdfBytes := []byte("%PDF-1.4 fake pdf bytes")
	pdfB64 := base64.StdEncoding.EncodeToString(pdfBytes)

	body := []byte(`[
		{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"` + pdfB64 + `"}},
		{"type":"document","source":{"type":"text","media_type":"text/plain","data":"hello world"}},
		{"type":"document","source":{"type":"url","url":"https://example.com/doc.pdf"}},
		{"type":"text","text":"summarize"}
	]`)

	var blocks []ContentBlockParam
	if err := json.Unmarshal(body, &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	msg, err := toMessage(MessageParam{Role: MessageRoleUser, Content: blocksToAny(blocks)})
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
	if string(files[1].Content) != "https://example.com/doc.pdf" {
		t.Errorf("url doc: got %q", string(files[1].Content))
	}

	// "hello world" text doc + the trailing "summarize" text block.
	if len(texts) != 2 || texts[0] != "hello world" || texts[1] != "summarize" {
		t.Errorf("expected texts [hello world, summarize], got %v", texts)
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
