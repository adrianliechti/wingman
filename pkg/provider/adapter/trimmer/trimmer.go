package trimmer

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Completer = (*Trimmer)(nil)

type Trimmer struct {
	*Config
	completer provider.Completer
}

type turn struct {
	messages []provider.Message
}

func New(completer provider.Completer, opts ...Option) *Trimmer {
	cfg := &Config{
		tokenThreshold: 128000,
		keepTurns:      4,
		keepToolUses:   3,
		charsPerToken:  3,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	return &Trimmer{
		Config:    cfg,
		completer: completer,
	}
}

func (t *Trimmer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if t.estimateTokens(messages) > t.tokenThreshold {
		messages = t.trim(messages)
	}

	return t.completer.Complete(ctx, t.filterEmpty(messages), options)
}

func (t *Trimmer) trim(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return messages
	}

	turns := t.groupTurns(messages)

	// Phase 1: progressively clear tool results
	for keepTools := t.keepToolUses; keepTools >= 1; keepTools-- {
		if result := t.finalize(t.clearToolResults(turns, keepTools)); t.estimateTokens(result) <= t.tokenThreshold {
			return result
		}
	}

	// Phase 2: remove old non-system turns until under threshold
	turns = t.clearToolResults(turns, 1)

	for {
		idx := t.firstRemovableTurn(turns)
		if idx < 0 {
			break
		}

		turns = append(turns[:idx], turns[idx+1:]...)

		if result := t.finalize(turns); t.estimateTokens(result) <= t.tokenThreshold {
			return result
		}
	}

	return t.finalize(turns)
}

func (t *Trimmer) finalize(turns []turn) []provider.Message {
	return t.fixBrokenPairs(t.flatten(turns))
}

func (t *Trimmer) groupTurns(messages []provider.Message) []turn {
	var turns []turn
	var current turn

	pending := make(map[string]bool)

	for _, m := range messages {
		for _, c := range m.Content {
			if c.ToolCall != nil {
				pending[c.ToolCall.ID] = true
			}

			if c.ToolResult != nil {
				delete(pending, c.ToolResult.ID)
			}
		}

		current.messages = append(current.messages, m)

		if len(pending) == 0 {
			turns = append(turns, current)
			current = turn{}
		}
	}

	if len(current.messages) > 0 {
		turns = append(turns, current)
	}

	return turns
}

func (t *Trimmer) flatten(turns []turn) []provider.Message {
	var result []provider.Message

	for _, trn := range turns {
		result = append(result, trn.messages...)
	}

	return result
}

func (t *Trimmer) isSystemTurn(trn turn) bool {
	for _, m := range trn.messages {
		if m.Role != provider.MessageRoleSystem {
			return false
		}
	}

	return true
}

func (t *Trimmer) countNonSystemTurns(turns []turn) int {
	count := 0

	for _, trn := range turns {
		if !t.isSystemTurn(trn) {
			count++
		}
	}

	return count
}

func (t *Trimmer) firstRemovableTurn(turns []turn) int {
	nonSystemCount := t.countNonSystemTurns(turns)

	if nonSystemCount <= 1 {
		return -1
	}

	for i, trn := range turns {
		if !t.isSystemTurn(trn) {
			return i
		}
	}

	return -1
}

func (t *Trimmer) clearToolResults(turns []turn, keepToolUses int) []turn {
	if len(turns) == 0 {
		return turns
	}

	protected := make([]bool, len(turns))
	toolCount := 0

	for i := len(turns) - 1; i >= 0; i-- {
		if i >= len(turns)-t.keepTurns {
			protected[i] = true
			continue
		}

		if t.hasToolContent(turns[i]) && toolCount < keepToolUses {
			protected[i] = true
			toolCount++
		}
	}

	result := make([]turn, len(turns))

	for i, trn := range turns {
		if protected[i] {
			result[i] = trn
			continue
		}

		result[i] = t.clearTurnToolResults(trn)
	}

	return result
}

func (t *Trimmer) hasToolContent(trn turn) bool {
	for _, m := range trn.messages {
		for _, c := range m.Content {
			if c.ToolCall != nil || c.ToolResult != nil {
				return true
			}
		}
	}

	return false
}

func (t *Trimmer) clearTurnToolResults(trn turn) turn {
	newMessages := make([]provider.Message, len(trn.messages))

	for i, m := range trn.messages {
		newContent := make([]provider.Content, 0, len(m.Content))

		for _, c := range m.Content {
			if c.ToolResult == nil {
				newContent = append(newContent, c)
				continue
			}

			newContent = append(newContent, provider.Content{
				ToolResult: &provider.ToolResult{
					ID:   c.ToolResult.ID,
					Data: "[cleared]",
				},
			})
		}

		newMessages[i] = provider.Message{
			Role:    m.Role,
			Content: newContent,
		}
	}

	return turn{messages: newMessages}
}

func (t *Trimmer) filterEmpty(messages []provider.Message) []provider.Message {
	result := make([]provider.Message, 0, len(messages))

	for _, m := range messages {
		if t.hasContent(m) {
			result = append(result, m)
		}
	}

	return result
}

func (t *Trimmer) hasContent(m provider.Message) bool {
	for _, c := range m.Content {
		if c.Text != "" || c.File != nil {
			return true
		}

		if c.ToolCall != nil && c.ToolCall.ID != "" && c.ToolCall.Name != "" {
			return true
		}

		if c.ToolResult != nil && c.ToolResult.ID != "" {
			return true
		}
	}

	return false
}

func (t *Trimmer) estimateTokens(messages []provider.Message) int {
	total := 0

	for _, m := range messages {
		total += 4

		for _, c := range m.Content {
			total += t.tokenCount(c.Text)

			if c.File != nil {
				total += len(c.File.Content) / t.charsPerToken
			}

			if c.ToolCall != nil {
				total += t.tokenCount(c.ToolCall.Name+c.ToolCall.Arguments) + 4
			}

			if c.ToolResult != nil {
				total += t.tokenCount(c.ToolResult.Data) + 4
			}
		}
	}

	return total
}

func (t *Trimmer) tokenCount(s string) int {
	if s == "" {
		return 0
	}

	return (len(s) + t.charsPerToken - 1) / t.charsPerToken
}

func (t *Trimmer) fixBrokenPairs(messages []provider.Message) []provider.Message {
	calls := make(map[string]bool)
	results := make(map[string]bool)

	for _, m := range messages {
		for _, c := range m.Content {
			if c.ToolCall != nil {
				calls[c.ToolCall.ID] = true
			}

			if c.ToolResult != nil {
				results[c.ToolResult.ID] = true
			}
		}
	}

	orphanCalls := make(map[string]bool)
	orphanResults := make(map[string]bool)

	for id := range calls {
		if !results[id] {
			orphanCalls[id] = true
		}
	}

	for id := range results {
		if !calls[id] {
			orphanResults[id] = true
		}
	}

	if len(orphanCalls) == 0 && len(orphanResults) == 0 {
		return messages
	}

	result := make([]provider.Message, 0, len(messages))

	for _, m := range messages {
		newContent := make([]provider.Content, 0, len(m.Content))

		for _, c := range m.Content {
			if c.ToolCall != nil && orphanCalls[c.ToolCall.ID] {
				continue
			}

			if c.ToolResult != nil && orphanResults[c.ToolResult.ID] {
				continue
			}

			newContent = append(newContent, c)
		}

		if len(newContent) > 0 {
			result = append(result, provider.Message{
				Role:    m.Role,
				Content: newContent,
			})
		}
	}

	return result
}
