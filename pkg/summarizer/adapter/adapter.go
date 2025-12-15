package adapter

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/summarizer"
	"github.com/adrianliechti/wingman/pkg/text"
)

var _ summarizer.Provider = (*Adapter)(nil)

type Adapter struct {
	completer provider.Completer
}

func FromCompleter(completer provider.Completer) *Adapter {
	return &Adapter{
		completer: completer,
	}
}

func (a *Adapter) Summarize(ctx context.Context, content string, options *summarizer.SummarizerOptions) (*summarizer.Summary, error) {
	splitter := text.NewSplitter()
	splitter.ChunkSize = 16000
	splitter.ChunkOverlap = 0

	var segments []string

	for _, part := range splitter.Split(content) {
		acc := provider.CompletionAccumulator{}

		for completion, err := range a.completer.Complete(ctx, []provider.Message{
			provider.UserMessage("Write a concise summary of the following: \n" + part),
		}, nil) {
			if err != nil {
				return nil, err
			}

			acc.Add(*completion)
		}

		result := acc.Result()
		segments = append(segments, result.Message.Text())
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range a.completer.Complete(ctx, []provider.Message{
		provider.UserMessage("Distill the following parts into a consolidated summary: \n" + strings.Join(segments, "\n\n")),
	}, nil) {
		if err != nil {
			return nil, err
		}

		acc.Add(*completion)
	}

	finalResult := acc.Result()

	result := &summarizer.Summary{
		Text: finalResult.Message.Text(),
	}

	return result, nil
}
