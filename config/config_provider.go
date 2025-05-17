package config

import (
	"errors"

	"github.com/adrianliechti/wingman/pkg/limiter"
	"github.com/adrianliechti/wingman/pkg/otel"

	reranker "github.com/adrianliechti/wingman/pkg/provider/adapter/reranker"
	summarizer "github.com/adrianliechti/wingman/pkg/summarizer/adapter"

	"gopkg.in/yaml.v3"
)

func (cfg *Config) registerProviders(f *configFile) error {
	for _, p := range f.Providers {
		models := map[string]modelConfig{}

		if err := p.Models.Decode(&models); err != nil {
			var ids []string

			if err := p.Models.Decode(&ids); err != nil {
				return err
			}

			for _, id := range ids {
				models[id] = modelConfig{
					ID: id,
				}
			}
		}

		for _, node := range p.Models.Content {
			id := node.Value

			if id == "" {
				continue
			}

			m, ok := models[id]

			if !ok {
				continue
			}

			if m.ID == "" {
				m.ID = id
			}

			if m.Type == "" {
				m.Type = DetectModelType(m.ID)
			}

			if m.Type == "" {
				m.Type = DetectModelType(id)
			}

			limit := m.Limit

			if limit == nil {
				limit = p.Limit
			}

			context := modelContext{
				ID: m.ID,

				Type: m.Type,

				Name:        m.Name,
				Description: m.Description,

				Limiter: createLimiter(limit),
			}

			switch context.Type {
			case ModelTypeCompleter:
				completer, err := createCompleter(p, context)

				if err != nil {
					return err
				}

				if _, ok := completer.(limiter.Completer); !ok {
					completer = limiter.NewCompleter(context.Limiter, completer)
				}

				if _, ok := completer.(otel.Completer); !ok {
					completer = otel.NewCompleter(p.Type, id, completer)
				}

				cfg.RegisterCompleter(id, completer)
				cfg.RegisterSummarizer(id, summarizer.FromCompleter(completer))

			case ModelTypeEmbedder:
				embedder, err := createEmbedder(p, context)

				if err != nil {
					return err
				}

				if _, ok := embedder.(limiter.Embedder); !ok {
					embedder = limiter.NewEmbedder(context.Limiter, embedder)
				}

				if _, ok := embedder.(otel.Embedder); !ok {
					embedder = otel.NewEmbedder(p.Type, id, embedder)
				}

				cfg.RegisterEmbedder(id, embedder)
				cfg.RegisterReranker(id, reranker.FromEmbedder(id, embedder))

			case ModelTypeReranker:
				reranker, err := createReranker(p, context)

				if err != nil {
					return err
				}

				if _, ok := reranker.(limiter.Reranker); !ok {
					reranker = limiter.NewReranker(context.Limiter, reranker)
				}

				if _, ok := reranker.(otel.Reranker); !ok {
					reranker = otel.NewReranker(p.Type, id, reranker)
				}

				cfg.RegisterReranker(id, reranker)

			case ModelTypeRenderer:
				renderer, err := createRenderer(p, context)

				if err != nil {
					return err
				}

				if _, ok := renderer.(limiter.Renderer); !ok {
					renderer = limiter.NewRenderer(context.Limiter, renderer)
				}

				if _, ok := renderer.(otel.Renderer); !ok {
					renderer = otel.NewRenderer(p.Type, id, renderer)
				}

				cfg.RegisterRenderer(id, renderer)

			case ModelTypeSynthesizer:
				synthesizer, err := createSynthesizer(p, context)

				if err != nil {
					return err
				}

				if _, ok := synthesizer.(limiter.Synthesizer); !ok {
					synthesizer = limiter.NewSynthesizer(context.Limiter, synthesizer)
				}

				if _, ok := synthesizer.(otel.Synthesizer); !ok {
					synthesizer = otel.NewSynthesizer(p.Type, id, synthesizer)
				}

				cfg.RegisterSynthesizer(id, synthesizer)

			case ModelTypeTranscriber:
				transcriber, err := createTranscriber(p, context)

				if err != nil {
					return err
				}

				if _, ok := transcriber.(limiter.Transcriber); !ok {
					transcriber = limiter.NewTranscriber(context.Limiter, transcriber)
				}

				if _, ok := transcriber.(otel.Transcriber); !ok {
					transcriber = otel.NewTranscriber(p.Type, id, transcriber)
				}

				cfg.RegisterTranscriber(id, transcriber)

			default:
				return errors.New("invalid model type: " + id)
			}
		}
	}

	return nil
}

type providerConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Limit *int `yaml:"limit"`

	Models yaml.Node `yaml:"models"`
}
