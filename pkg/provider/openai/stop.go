package openai

import (
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/toolid"
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
	stops  []string
	buffer strings.Builder
	done   bool
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
	if f == nil || f.done || c == nil || c.Message == nil {
		return c, false
	}

	for i, content := range c.Message.Content {
		if content.Text == "" {
			continue
		}

		before := f.buffer.Len()
		f.buffer.WriteString(content.Text)

		index, stop := firstStop(f.buffer.String(), f.stops)
		if index < 0 {
			continue
		}

		f.done = true

		cut := max(index-before, 0)

		out := *c
		out.Status = provider.CompletionStatusCompleted
		out.StopReason = provider.StopReasonStopSequence
		out.StopSequence = stop

		kept := append([]provider.Content{}, c.Message.Content[:i]...)
		if cut > 0 {
			kept = append(kept, provider.Content{Text: content.Text[:cut], Phase: content.Phase})
		}

		out.Message = &provider.Message{
			Role:    c.Message.Role,
			Phase:   c.Message.Phase,
			Content: kept,
		}

		return &out, true
	}

	return c, false
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
