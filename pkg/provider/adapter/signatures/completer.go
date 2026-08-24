package signatures

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/toolid"
)

var _ provider.Completer = (*Completer)(nil)

// Completer strips provider-bound reasoning state (OpenAI encrypted content,
// Anthropic thinking signatures, and Gemini signatures embedded in tool IDs)
// so histories stay portable across providers or projects that cannot verify
// each other's opaque state. Compaction is disabled because its history cannot
// be resumed without the opaque continuation blob.
type Completer struct {
	completer provider.Completer
}

func FromCompleter(completer provider.Completer) *Completer {
	return &Completer{
		completer: completer,
	}
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	messages = stripMessages(messages)
	options = stripOptions(options)

	return func(yield func(*provider.Completion, error) bool) {
		for completion, err := range c.completer.Complete(ctx, messages, options) {
			if completion != nil && completion.Message != nil {
				completion.Message.Content = stripContents(completion.Message.Content)
			}

			if !yield(completion, err) {
				return
			}
		}
	}
}

func stripMessages(messages []provider.Message) []provider.Message {
	result := make([]provider.Message, 0, len(messages))

	for _, m := range messages {
		contents := stripContents(m.Content)

		if len(m.Content) > 0 && len(contents) == 0 {
			continue
		}

		m.Content = contents
		result = append(result, m)
	}

	return result
}

func stripContents(contents []provider.Content) []provider.Content {
	result := make([]provider.Content, 0, len(contents))

	for _, c := range contents {
		if c.Reasoning != nil {
			if c.Reasoning.Redacted {
				continue
			}

			if c.Reasoning.Signature != "" {
				reasoning := *c.Reasoning
				reasoning.Signature = ""

				if reasoning.Text == "" && reasoning.Summary == "" {
					continue
				}

				c.Reasoning = &reasoning
			}
		}

		if c.Compaction != nil && c.Compaction.Signature != "" {
			compaction := *c.Compaction
			compaction.Signature = ""

			if compaction.Content == "" {
				continue
			}

			c.Compaction = &compaction
		}

		if c.ToolCall != nil {
			call := *c.ToolCall
			call.ID = toolid.StripSignature(call.ID)
			c.ToolCall = &call
		}

		if c.ToolResult != nil {
			result := *c.ToolResult
			result.ID = toolid.StripSignature(result.ID)
			c.ToolResult = &result
		}

		result = append(result, c)
	}

	return result
}

func stripOptions(options *provider.CompleteOptions) *provider.CompleteOptions {
	if options == nil {
		return options
	}

	includeSignature := options.ReasoningOptions != nil && options.ReasoningOptions.IncludeSignature
	if !includeSignature && options.CompactionOptions == nil {
		return options
	}

	copy := *options

	if includeSignature {
		reasoning := *options.ReasoningOptions
		reasoning.IncludeSignature = false
		copy.ReasoningOptions = &reasoning
	}

	// Without the opaque compaction blob, the next turn cannot reconstruct the
	// compacted history. Prevent creating a continuation that would lose it.
	copy.CompactionOptions = nil

	return &copy
}
