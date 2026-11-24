package openai

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/internal/toolid"
)

// OpenAI call ids are limited to 64 characters. Ids that arrive from other
// backends may carry embedded state (Gemini packs a thought signature into
// "id::name::sig"); trim them to the id on both the call and the result so
// the pair still matches.
const maxCallID = 64

func sanitizeToolIDs(messages []provider.Message) []provider.Message {
	result := make([]provider.Message, 0, len(messages))

	for _, m := range messages {
		content := make([]provider.Content, 0, len(m.Content))

		for _, c := range m.Content {
			if c.ToolCall != nil {
				call := *c.ToolCall
				call.ID = toolid.Sanitize(call.ID, maxCallID)
				c.ToolCall = &call
			}

			if c.ToolResult != nil {
				res := *c.ToolResult
				res.ID = toolid.Sanitize(res.ID, maxCallID)
				c.ToolResult = &res
			}

			content = append(content, c)
		}

		m.Content = content
		result = append(result, m)
	}

	return result
}

// stopFilter emulates stop sequences for backends without a native
// parameter (the Responses API, and reasoning models on Chat Completions):
// the text stream is cut at the first match and the completion ends with
// stop_sequence. The upstream stream is cancelled by the caller.
type stopFilter struct {
	stops   []string
	pending *provider.Content
	done    bool
}

func newStopFilter(stops []string) *stopFilter {
	if len(stops) == 0 {
		return nil
	}

	return &stopFilter{stops: stops}
}

// filter returns the completion to forward and whether a stop sequence was
// hit. A hit completion carries the text up to the match and the final
// stop state; nothing must be forwarded after it.
func (f *stopFilter) filter(c *provider.Completion) (*provider.Completion, bool) {
	if f == nil || f.done || c == nil {
		return c, false
	}

	out := *c
	message := provider.Message{Role: provider.MessageRoleAssistant}
	if c.Message != nil {
		message = *c.Message
	}
	var kept []provider.Content
	flush := func() {
		if f.pending != nil {
			kept = append(kept, *f.pending)
			f.pending = nil
		}
	}
	for _, content := range message.Content {
		// Do not join text across message items or non-text content blocks.
		if content.Text == "" || (f.pending != nil && content.MessageID != "" && content.MessageID != f.pending.MessageID) {
			flush()
		}
		if content.Text == "" {
			kept = append(kept, content)
			continue
		}
		text := content.Text
		if f.pending != nil {
			text = f.pending.Text + text
			content = *f.pending
			f.pending = nil
		}
		if index, stop := firstStop(text, f.stops); index >= 0 {
			f.done = true
			out.Status = provider.CompletionStatusCompleted
			out.StopReason = provider.StopReasonStopSequence
			out.StopSequence = stop
			if index > 0 {
				content.Text = text[:index]
				kept = append(kept, content)
			}
			break
		}
		// Keep just the suffix that could become a stop on the next delta.
		// Already emitted text can never be removed from a client's stream.
		keep := len(text) - f.prefixLength(text)
		if keep < len(text) {
			pending := content
			pending.Text = text[keep:]
			f.pending = &pending
		}
		if keep > 0 || content.TextPresent {
			content.Text = text[:keep]
			kept = append(kept, content)
		}
	}
	if c.Status != "" || c.StopReason != "" {
		flush()
	}
	if c.Message != nil || len(kept) > 0 {
		message.Content = kept
		out.Message = &message
	}
	// A corrected terminal message can introduce a stop absent from its deltas.
	if c.Output != nil {
		final := &provider.Completion{Status: c.Status, Message: &provider.Message{Content: c.Output}}
		filtered, hit := newStopFilter(f.stops).filter(final)
		out.Output = append([]provider.Content{}, filtered.Message.Content...)
		if hit {
			out.StopReason, out.StopSequence, out.Status = filtered.StopReason, filtered.StopSequence, filtered.Status
			f.done = true
		}
	}
	return &out, f.done
}

func (f *stopFilter) prefixLength(text string) int {
	longest := 0
	for _, stop := range f.stops {
		for n := min(len(text), len(stop)-1); n > longest; n-- {
			if strings.HasSuffix(text, stop[:n]) {
				longest = n
				break
			}
		}
	}
	return longest
}

func firstStop(text string, stops []string) (int, string) {
	index := -1
	match := ""

	for _, stop := range stops {
		if stop == "" {
			continue
		}

		if i := strings.Index(text, stop); i >= 0 && (index < 0 || i < index) {
			index, match = i, stop
		}
	}

	return index, match
}
