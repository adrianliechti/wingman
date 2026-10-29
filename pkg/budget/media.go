package budget

import (
	"context"
	"iter"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type reranker struct {
	reporter
	provider.Reranker
}

func NewReranker(name, model string, p provider.Reranker, b Provider) provider.Reranker {
	return &reranker{reporter{b, name, model}, p}
}
func (p *reranker) Rerank(ctx context.Context, query string, texts []string, options *provider.RerankOptions) ([]provider.Ranking, error) {
	r, err := p.start(ctx)
	if err != nil {
		return nil, err
	}
	defer p.finish(ctx, r)
	// The native interface exposes no token usage; report the operation with
	// unknown usage rather than inventing billable counts.
	return p.Reranker.Rerank(ctx, query, texts, options)
}

type renderer struct {
	reporter
	provider.Renderer
}

func NewRenderer(name, model string, p provider.Renderer, b Provider) provider.Renderer {
	return &renderer{reporter{b, name, model}, p}
}
func (p *renderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	r, err := p.start(ctx)
	if err != nil {
		return nil, err
	}
	defer p.finish(ctx, r)
	result, err := p.Renderer.Render(ctx, input, options)
	if result != nil {
		r.observe(result.Model, nil)
	}
	return result, err
}

type synthesizer struct {
	reporter
	provider.Synthesizer
}

func NewSynthesizer(name, model string, p provider.Synthesizer, b Provider) provider.Synthesizer {
	return &synthesizer{reporter{b, name, model}, p}
}
func (p *synthesizer) Synthesize(ctx context.Context, input string, options *provider.SynthesizeOptions) iter.Seq2[*provider.Synthesis, error] {
	return func(yield func(*provider.Synthesis, error) bool) {
		r, err := p.start(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		defer p.finish(ctx, r)
		for result, err := range p.Synthesizer.Synthesize(ctx, input, options) {
			if result != nil {
				r.observe(result.Model, nil)
			}
			if !yield(result, err) || err != nil {
				return
			}
		}
	}
}

type transcriber struct {
	reporter
	provider.Transcriber
}

func NewTranscriber(name, model string, p provider.Transcriber, b Provider) provider.Transcriber {
	return &transcriber{reporter{b, name, model}, p}
}
func (p *transcriber) Transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) iter.Seq2[*provider.Transcription, error] {
	return func(yield func(*provider.Transcription, error) bool) {
		r, err := p.start(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		defer p.finish(ctx, r)
		for result, err := range p.Transcriber.Transcribe(ctx, input, options) {
			if result != nil {
				r.observe(result.Model, nil)
			}
			if !yield(result, err) || err != nil {
				return
			}
		}
	}
}
