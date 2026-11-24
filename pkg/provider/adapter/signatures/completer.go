// Package signatures binds opaque reasoning state (thinking signatures,
// encrypted reasoning, compaction blobs) to the model that produced it.
package signatures

import (
	"context"
	"iter"
	"maps"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/internal/toolid"
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
		input := messages
		for attempt := 0; attempt < 2; attempt++ {
			emitted, retry := false, false
			for completion, err := range c.completer.Complete(ctx, input, options) {
				// Never restart a response after forwarding a completion, and
				// never discard compaction state to recover a reasoning failure.
				if err != nil && completion == nil && !emitted && ctx.Err() == nil && reasoningReplayError(err) {
					if stripped, changed := withoutReasoningSignatures(input); changed {
						input, retry = stripped, true
						break
					}
				}
				if completion != nil {
					emitted = true
					completion = c.scopeCompletion(completion)
				}
				if !yield(completion, err) {
					return
				}
			}
			if !retry {
				return
			}
		}
	}
}

func (c *Completer) scopeCompletion(original *provider.Completion) *provider.Completion {
	if original.Message == nil && original.Output == nil && len(original.ReasoningSnapshots) == 0 {
		return original
	}
	scoped := *original
	scope := tagContents
	if c.realm == "" {
		scope = resolveContents
	}
	if original.Message != nil {
		message := *original.Message
		message.Content = scope(message.Content, c.realm)
		scoped.Message = &message
	}
	if original.Output != nil {
		scoped.Output = scope(original.Output, c.realm)
	}
	if original.ReasoningSnapshots != nil {
		scoped.ReasoningSnapshots = maps.Clone(original.ReasoningSnapshots)
		for id, snapshot := range scoped.ReasoningSnapshots {
			if c.realm == "" {
				snapshot.Signature = ""
			} else {
				snapshot.Signature = provider.TagSignature(c.realm, snapshot.Signature)
			}
			scoped.ReasoningSnapshots[id] = snapshot
		}
	}
	if c.realm == "" && original.Message != nil {
		// No item in this realm can acquire a replay token, even if the
		// upstream never sends one. Release it on its first identified delta.
		for _, part := range original.Message.Content {
			if r := part.Reasoning; r != nil && r.ID != "" {
				if _, ok := scoped.ReasoningSnapshots[r.ID]; !ok {
					if scoped.ReasoningSnapshots == nil {
						scoped.ReasoningSnapshots = make(provider.ReasoningSnapshots)
					}
					scoped.ReasoningSnapshots[r.ID] = provider.Reasoning{ID: r.ID}
				}
			}
		}
	}
	return &scoped
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
	return mapMessages(messages, func(contents []provider.Content) []provider.Content {
		return resolveContents(contents, realm)
	})
}

// mapMessages rewrites every message's contents and drops turns left empty,
// e.g. an assistant message holding only a foreign compaction block.
func mapMessages(messages []provider.Message, rewrite func([]provider.Content) []provider.Content) []provider.Message {
	result := make([]provider.Message, 0, len(messages))

	for _, m := range messages {
		contents := rewrite(m.Content)

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
			} else if reasoning, ok = unsigned(reasoning); !ok {
				continue
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

// Only known input-replay failures qualify; auth, rate limits, and transport
// errors must retain their normal error/retry policy.
func reasoningReplayError(err error) bool {
	// Rejected replay state is a request error, reported by some backends
	// without a status.
	switch provider.KindFromError(err) {
	case "", provider.ErrorKindInvalidArgument, provider.ErrorKindUnprocessable, provider.ErrorKindPayloadTooLarge:
	default:
		return false
	}
	if provider.TypeFromError(err) == "invalid_encrypted_content" {
		return true
	}

	message := strings.ToLower(strings.ReplaceAll(err.Error(), "`", ""))

	// Every fragment of a group must appear for the message to qualify.
	for _, fragments := range [][]string{
		{"invalid_encrypted_content"},
		{"encrypted content could not be verified"},
		{"reasoning", "required following item"},
		{"thinking", "invalid signature"},
		{"thinking", "signature verification failed"},
		{"the final block in an assistant message cannot be thinking"},
	} {
		matched := true

		for _, fragment := range fragments {
			matched = matched && strings.Contains(message, fragment)
		}

		if matched {
			return true
		}
	}

	return false
}

// unsigned drops a reasoning block's signature. Text survives on its own; a
// redacted block is nothing but its blob and is not kept.
func unsigned(reasoning provider.Reasoning) (provider.Reasoning, bool) {
	reasoning.Signature = ""

	if reasoning.Redacted || reasoning.Text() == "" && reasoning.SummaryText() == "" {
		return reasoning, false
	}

	return reasoning, true
}

// withoutReasoningSignatures removes every reasoning signature from a replay
// history, exactly as foreign signatures are removed on resolve.
func withoutReasoningSignatures(messages []provider.Message) ([]provider.Message, bool) {
	changed := false

	result := mapMessages(messages, func(contents []provider.Content) []provider.Content {
		result := make([]provider.Content, 0, len(contents))

		for _, c := range contents {
			if c.Reasoning != nil && c.Reasoning.Signature != "" {
				changed = true

				reasoning, ok := unsigned(*c.Reasoning)
				if !ok {
					continue
				}

				c.Reasoning = &reasoning
			}

			result = append(result, c)
		}

		return result
	})

	return result, changed
}
