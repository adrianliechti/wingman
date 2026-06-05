package router

import (
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
)

func ScrubMessages(messages []provider.Message) []provider.Message {
	result := make([]provider.Message, len(messages))

	for i, m := range messages {
		result[i] = provider.Message{
			Role:    m.Role,
			Content: scrubContent(m.Content),
		}
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
	if options == nil || options.ReasoningOptions == nil {
		return options
	}

	if !options.ReasoningOptions.IncludeSignature {
		return options
	}

	cloned := *options
	reasoning := *options.ReasoningOptions
	reasoning.IncludeSignature = false
	cloned.ReasoningOptions = &reasoning

	return &cloned
}
