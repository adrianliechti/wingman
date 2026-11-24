package provider

import "testing"

func TestTagParseSignature(t *testing.T) {
	tagged := TagSignature("claude-sonnet-4-6", "abc:def")

	if tagged != "@claude-sonnet-4-6:abc:def" {
		t.Fatalf("tagged: %q", tagged)
	}

	realm, raw := ParseSignature(tagged)
	if realm != "claude-sonnet-4-6" || raw != "abc:def" {
		t.Errorf("parse: %q %q", realm, raw)
	}

	if realm, raw := ParseSignature("plain"); realm != "" || raw != "plain" {
		t.Errorf("untagged: %q %q", realm, raw)
	}

	if TagSignature("gpt-5.4", "") != "" {
		t.Error("empty signature must stay empty")
	}
}

func TestParseSignaturePreservesBinaryValues(t *testing.T) {
	for _, value := range []string{
		"@\x01:signature",
		"@\xff:signature",
		"@model:\x80\xff\x00",
		"@\x01:\x80\xff\x00",
	} {
		t.Run(value, func(t *testing.T) {
			if realm, raw := ParseSignature(value); realm != "" || raw != value {
				t.Fatalf("binary signature parsed as tagged: realm=%q raw=%q", realm, raw)
			}
		})
	}
}

func TestReasoningIDSignature(t *testing.T) {
	if got := WrapReasoningID("", "SIG"); got != "SIG" {
		t.Fatalf("expected bare signature without id, got %q", got)
	}

	if got := WrapReasoningID("rs_1", ""); got != "" {
		t.Fatalf("expected empty for missing signature, got %q", got)
	}

	id, signature := UnwrapReasoningID(WrapReasoningID("rs_1", "BLOB"))
	if id != "rs_1" || signature != "BLOB" {
		t.Fatalf("round-trip mismatch: %q %q", id, signature)
	}

	id, signature = UnwrapReasoningID("RAW")
	if id != "" || signature != "RAW" {
		t.Fatalf("expected raw value as signature, got %q %q", id, signature)
	}

	tagged := WrapReasoningID("rs_1", "@gpt-5:BLOB")
	if realm, raw := ParseSignature(tagged); realm != "gpt-5" || len(raw) == 0 {
		t.Fatalf("expected outer realm tag on wrapped signature, got %q (realm=%q raw=%q)", tagged, realm, raw)
	}
	if id, signature = UnwrapReasoningID(tagged); id != "rs_1" || signature != "@gpt-5:BLOB" {
		t.Fatalf("tagged round-trip mismatch: %q %q", id, signature)
	}
}
