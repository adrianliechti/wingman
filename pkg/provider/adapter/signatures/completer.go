// Package signatures binds opaque reasoning state (thinking signatures,
// encrypted reasoning, compaction blobs) to the model that produced it.
package signatures

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/toolid"
)

var _ provider.Completer = (*Completer)(nil)

// Completer scopes signatures to a realm, normally the configured model id.
// Signatures the wrapped completer emits are tagged with the realm; on
// replay, only signatures of the same realm (or untagged ones) reach the
// completer, everything else is removed. A client that switches models
// mid-conversation therefore never replays one backend's opaque state into
// another — the worst case is one turn without thinking continuity.
//
// With an empty realm nothing is ever replayed or emitted: signatures are
// stripped in both directions, Gemini tool-id signatures are removed, and
// compaction is disabled because its history cannot be resumed without the
// blob.
type Completer struct {
	realm     string
	completer provider.Completer
}

// ScopedTo wraps a completer with the realm it is registered under.
func ScopedTo(realm string, completer provider.Completer) *Completer {
	return &Completer{
		realm:     realm,
		completer: completer,
	}
}

// FromCompleter wraps a completer in strip mode.
func FromCompleter(completer provider.Completer) *Completer {
	return ScopedTo("", completer)
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	messages = resolveMessages(messages, c.realm)

	if c.realm == "" {
		options = stripOptions(options)
	}

	return func(yield func(*provider.Completion, error) bool) {
		for completion, err := range c.completer.Complete(ctx, messages, options) {
			if completion != nil && completion.Message != nil {
				if c.realm == "" {
					completion.Message.Content = resolveContents(completion.Message.Content, "")
				} else {
					completion.Message.Content = tagContents(completion.Message.Content, c.realm)
				}
			}

			if !yield(completion, err) {
				return
			}
		}
	}
}

// StripToolSignatures removes Gemini thought signatures embedded in tool ids;
// those are not realm-tagged and only verify on the project that issued them.
func StripToolSignatures(messages []provider.Message) []provider.Message {
	result := make([]provider.Message, 0, len(messages))

	for _, m := range messages {
		m.Content = stripToolIDs(m.Content)
		result = append(result, m)
	}

	return result
}

func resolveMessages(messages []provider.Message, realm string) []provider.Message {
	result := make([]provider.Message, 0, len(messages))

	for _, m := range messages {
		contents := resolveContents(m.Content, realm)

		// e.g. an assistant message holding only a foreign compaction block
		if len(m.Content) > 0 && len(contents) == 0 {
			continue
		}

		m.Content = contents
		result = append(result, m)
	}

	return result
}

// own reports whether a signature may be replayed into the realm and returns
// its raw value.
func own(realm, value string) (string, bool) {
	origin, raw := provider.ParseSignature(value)

	if realm == "" {
		return "", false
	}

	return raw, origin == "" || origin == realm
}

// resolveContents prepares contents for replay into a realm: own signatures
// are unwrapped, foreign ones removed. Reasoning text and compaction
// summaries survive without their signature; a redacted block or a
// summary-less compaction is nothing but its blob and is dropped. In strip
// mode (empty realm) tool-id signatures are removed as well.
func resolveContents(contents []provider.Content, realm string) []provider.Content {
	result := make([]provider.Content, 0, len(contents))

	for _, c := range contents {
		if c.Reasoning != nil && c.Reasoning.Signature != "" {
			reasoning := *c.Reasoning

			if raw, ok := own(realm, reasoning.Signature); ok {
				reasoning.Signature = raw
			} else if reasoning.Redacted {
				continue
			} else {
				reasoning.Signature = ""

				if reasoning.Text == "" && reasoning.Summary == "" {
					continue
				}
			}

			c.Reasoning = &reasoning
		}

		if c.Compaction != nil && c.Compaction.Signature != "" {
			compaction := *c.Compaction

			if raw, ok := own(realm, compaction.Signature); ok {
				compaction.Signature = raw
			} else {
				compaction.Signature = ""

				if compaction.Content == "" {
					continue
				}
			}

			c.Compaction = &compaction
		}

		result = append(result, c)
	}

	if realm == "" {
		result = stripToolIDs(result)
	}

	return result
}

func tagContents(contents []provider.Content, realm string) []provider.Content {
	result := make([]provider.Content, 0, len(contents))

	for _, c := range contents {
		if c.Reasoning != nil && c.Reasoning.Signature != "" {
			reasoning := *c.Reasoning
			reasoning.Signature = provider.TagSignature(realm, reasoning.Signature)
			c.Reasoning = &reasoning
		}

		if c.Compaction != nil && c.Compaction.Signature != "" {
			compaction := *c.Compaction
			compaction.Signature = provider.TagSignature(realm, compaction.Signature)
			c.Compaction = &compaction
		}

		result = append(result, c)
	}

	return result
}

func stripToolIDs(contents []provider.Content) []provider.Content {
	result := make([]provider.Content, 0, len(contents))

	for _, c := range contents {
		if c.ToolCall != nil {
			call := *c.ToolCall
			call.ID = toolid.StripSignature(call.ID)
			c.ToolCall = &call
		}

		if c.ToolResult != nil {
			res := *c.ToolResult
			res.ID = toolid.StripSignature(res.ID)
			c.ToolResult = &res
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
