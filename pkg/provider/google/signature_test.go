package google

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestThoughtSignatureSurvivesJSON(t *testing.T) {
	raw := []byte{0x12, 0xff, 0x01, '\n', 0xe2, 0x82, 0x00, 0xc3}
	signature := EncodeThoughtSignature(raw)
	message := provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{Summary: provider.TextParts("why"), Signature: signature})}}
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var replay provider.Message
	if err := json.Unmarshal(data, &replay); err != nil {
		t.Fatal(err)
	}
	steps, err := convertMessages([]provider.Message{replay})
	if err != nil || len(steps) != 1 || steps[0].ThoughtStep == nil || !bytes.Equal(DecodeThoughtSignature(value(steps[0].ThoughtStep.Signature)), raw) {
		t.Fatalf("signature changed: %+v, %v", steps, err)
	}
}

func TestDecodeThoughtSignatureAcceptsRawBytes(t *testing.T) {
	if got := DecodeThoughtSignature("REAL_SIG"); string(got) != "REAL_SIG" {
		t.Fatalf("raw signature altered: %q", got)
	}
	if got := DecodeThoughtSignature(EncodeThoughtSignature([]byte{0xff, 0x00})); !bytes.Equal(got, []byte{0xff, 0x00}) {
		t.Fatalf("encoded signature not restored: %q", got)
	}
}

func TestStripToolIDSignature(t *testing.T) {
	for _, test := range []struct{ input, want string }{{"c1", "c1"}, {"c1::lookup", "c1::lookup"}, {"c1::lookup::Ev8BAA==", "c1::lookup"}, {"c1::lookup::", "c1::lookup"}} {
		if got := StripToolIDSignature(test.input); got != test.want {
			t.Errorf("StripToolIDSignature(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
