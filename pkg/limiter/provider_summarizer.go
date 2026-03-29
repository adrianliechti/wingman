package limiter

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/summarizer"

	"golang.org/x/time/rate"
)

type Summarizer interface {
	Limiter
	summarizer.Provider
}

type limitedSummarizer struct {
	limiter  *rate.Limiter
	provider summarizer.Provider
}

func NewSummarizer(l *rate.Limiter, p summarizer.Provider) Summarizer {
	return &limitedSummarizer{
		limiter:  l,
		provider: p,
	}
}

func (p *limitedSummarizer) limiterSetup() {
}

func (p *limitedSummarizer) Summarize(ctx context.Context, text string, options *summarizer.SummarizerOptions) (*summarizer.Summary, error) {
	if p.limiter != nil {
		p.limiter.Wait(ctx)
	}

	return p.provider.Summarize(ctx, text, options)
}
