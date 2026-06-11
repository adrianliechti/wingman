package router

import (
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
)

func ScrubMessages(messages []provider.Message) []provider.Message {
	result := make([]provider.Message, 0, len(messages))

	for _, m := range messages {
		content := scrubContent(m.Content)

		// e.g. an assistant message holding only a compaction block
		if len(content) == 0 {
			continue
		}

		result = append(result, provider.Message{
			Role:    m.Role,
			Content: content,
		})
	}

	return result
}

func scrubContent(content []provider.Content) []provider.Content {
	result := make([]provider.Content, 0, len(content))

	for _, c := range content {
		if c.Reasoning != nil || c.Compaction != nil {
			continue
		}

		if c.ToolCall != nil {
			call := *c.ToolCall
			call.ID = google.StripToolIDSignature(call.ID)
			c.ToolCall = &call
		}

		if c.ToolResult != nil {
			res := *c.ToolResult
			res.ID = google.StripToolIDSignature(res.ID)
			c.ToolResult = &res
		}

		result = append(result, c)
	}

	return result
}

func ScrubOptions(options *provider.CompleteOptions) *provider.CompleteOptions {
	if options == nil {
		return nil
	}

	includeSignature := options.ReasoningOptions != nil && options.ReasoningOptions.IncludeSignature

	if !includeSignature && options.CompactionOptions == nil {
		return options
	}

	cloned := *options

	if includeSignature {
		reasoning := *options.ReasoningOptions
		reasoning.IncludeSignature = false
		cloned.ReasoningOptions = &reasoning
	}

	// A compaction blob from one provider cannot round-trip through another;
	// scrubbing it next turn would silently drop the compacted history instead
	cloned.CompactionOptions = nil

	return &cloned
}
