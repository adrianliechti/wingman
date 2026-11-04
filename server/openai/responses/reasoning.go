package responses

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Responses has only one opaque reasoning field. Preserve the native redacted
// discriminator inside that field; ordinary signatures keep their wire format.
const redactedReasoningPrefix = "wingman:redacted:v1:"

func reasoningEncryptedContent(signature string, redacted bool) string {
	if !redacted || signature == "" {
		return signature
	}
	return redactedReasoningPrefix + base64.RawURLEncoding.EncodeToString([]byte(signature))
}

func parseReasoningEncryptedContent(value string) (string, bool, error) {
	encoded, ok := strings.CutPrefix(value, redactedReasoningPrefix)
	if !ok {
		return value, false, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return "", false, fmt.Errorf("invalid redacted reasoning content")
	}
	return string(data), true, nil
}

func reasoningSummaries(r *provider.Reasoning) []ReasoningOutputSummary {
	result := []ReasoningOutputSummary{}
	for _, part := range r.Summary {
		result = append(result, ReasoningOutputSummary{Type: "summary_text", Text: part.Text})
	}
	return result
}

// Use the same presentation for HTTP, item.done, and terminal SSE snapshots.
// Native summaries win; otherwise expose raw thinking as summary parts when
// requested, preserving boundaries and ignoring empty placeholder summaries.
func reasoningForOutput(r provider.Reasoning, summary bool) provider.Reasoning {
	if summary && r.SummaryText() == "" && r.Text() != "" {
		r.Summary, r.Content = r.Content, nil
	}
	return r
}

func reasoningOutputItem(r provider.Reasoning, status string) *ReasoningOutputItem {
	item := &ReasoningOutputItem{ID: r.ID, Type: "reasoning", Status: status,
		Summary: reasoningSummaries(&r), Content: []ReasoningOutputContentPart{},
		EncryptedContent: reasoningEncryptedContent(r.Signature, r.Redacted)}
	for _, part := range r.Content {
		item.Content = append(item.Content, ReasoningOutputContentPart{Type: "reasoning_text", Text: part.Text})
	}
	return item
}
