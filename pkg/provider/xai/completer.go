package xai

import (
	"context"
	"iter"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	responder *openai.Responder
}

func NewCompleter(model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		url:   "https://api.x.ai/v1/",
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	var ops []openai.Option

	if cfg.token != "" {
		ops = append(ops, openai.WithToken(cfg.token))
	}

	if cfg.client != nil {
		ops = append(ops, openai.WithClient(cfg.client))
	}

	responder, err := openai.NewResponder(cfg.url, cfg.model, ops...)

	if err != nil {
		return nil, err
	}

	return &Completer{
		responder: responder,
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	messages = consolidateSystemMessages(messages)
	return c.responder.Complete(ctx, messages, options)
}

// consolidateSystemMessages extracts all system messages from the message list,
// joins their text, and prepends a single system message. xAI does not support
// system/developer messages interspersed in the input.
func consolidateSystemMessages(messages []provider.Message) []provider.Message {
	var instructions []string
	var filtered []provider.Message

	for _, m := range messages {
		if m.Role == provider.MessageRoleSystem {
			if text := m.Text(); text != "" {
				instructions = append(instructions, text)
			}

			continue
		}

		filtered = append(filtered, m)
	}

	if len(instructions) == 0 {
		return messages
	}

	result := []provider.Message{
		provider.SystemMessage(strings.Join(instructions, "\n\n")),
	}

	return append(result, filtered...)
}
