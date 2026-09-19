package google

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
	"google.golang.org/genai"
)

// Gemini signatures are arbitrary bytes. Through the provider they must
// survive a JSON string, which the OpenAI and Anthropic surfaces use to carry
// them, and come back byte-identical for the Gemini wire.
func TestThoughtSignatureSurvivesJSON(t *testing.T) {
	raw := []byte{0x12, 0xff, 0x01, '\n', 0xe2, 0x82, 0x00, 0xc3}

	content := toContent(&genai.Content{Role: "model", Parts: []*genai.Part{{Text: "why", Thought: true, ThoughtSignature: raw}}}, nil, nil)
	if len(content) != 1 || content[0].Reasoning == nil || !utf8.ValidString(content[0].Reasoning.Signature) {
		t.Fatalf("signature is not wire-safe text: %+v", content)
	}

	data, err := json.Marshal(content[0].Reasoning.Signature)
	if err != nil {
		t.Fatal(err)
	}
	var wire string
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}

	message := provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{Text: "why", Signature: wire})}}
	parts, err := convertContent(message, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts.Parts) != 1 || !bytes.Equal(parts.Parts[0].ThoughtSignature, raw) {
		t.Fatalf("signature changed through JSON: %q, want %q", parts.Parts[0].ThoughtSignature, raw)
	}
}

// Histories built before signatures were encoded hold the raw bytes; they
// still reach Gemini unchanged.
func TestDecodeThoughtSignatureAcceptsRawBytes(t *testing.T) {
	if got := DecodeThoughtSignature("REAL_SIG"); string(got) != "REAL_SIG" {
		t.Fatalf("raw signature altered: %q", got)
	}
	if got := DecodeThoughtSignature(EncodeThoughtSignature([]byte{0xff, 0x00})); !bytes.Equal(got, []byte{0xff, 0x00}) {
		t.Fatalf("encoded signature not restored: %q", got)
	}
}
