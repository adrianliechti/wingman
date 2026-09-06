package reranker

import (
	"context"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type phasedCompleter struct{}

func (phasedCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for _, message := range []provider.Message{
			{Phase: provider.MessagePhaseCommentary, Content: []provider.Content{provider.TextContent(`{"rankings":[]}`)}},
			{Phase: provider.MessagePhaseFinalAnswer, Content: []provider.Content{provider.TextContent(`{"rankings":[{"index":0,"score":0.9}]}`)}},
		} {
			if !yield(&provider.Completion{Message: &message}, nil) {
				return
			}
		}
	}
}

func TestRerankParsesFinalAnswerAfterJSONCommentary(t *testing.T) {
	result, err := FromCompleter("model", phasedCompleter{}).Rerank(context.Background(), "query", []string{"document"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Text != "document" || result[0].Score != 0.9 {
		t.Fatalf("rankings = %+v", result)
	}
}
