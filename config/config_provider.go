package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
	reranker "github.com/adrianliechti/wingman/pkg/provider/adapter/reranker"

	"gopkg.in/yaml.v3"
)

func (cfg *Config) registerProviders(f *configFile) error {
	var firstReranker provider.Reranker

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

			maxRetries := m.MaxRetries

			if maxRetries == nil {
				maxRetries = p.MaxRetries
			}

			context := modelContext{
				ID: m.ID,

				Type: m.Type,

				Name:        m.Name,
				Description: m.Description,

				MaxRetries: maxRetries,
			}

			if p.Proxy != nil {
				client, err := p.Proxy.proxyClient()

				if err != nil {
					return err
				}

				context.Client = client
			}

			switch context.Type {
			case ModelTypeCompleter:
				completer, err := createCompleter(p, context)

				if err != nil {
					return err
				}

				if _, ok := completer.(otel.Completer); !ok {
					completer = otel.NewCompleter(p.Type, id, completer)
				}

				cfg.RegisterCompleter(id, completer)

			case ModelTypeEmbedder:
				embedder, err := createEmbedder(p, context)

				if err != nil {
					return err
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

				if _, ok := reranker.(otel.Reranker); !ok {
					reranker = otel.NewReranker(p.Type, id, reranker)
				}

				cfg.RegisterReranker(id, reranker)

				if firstReranker == nil {
					firstReranker = reranker
				}

			case ModelTypeRenderer:
				renderer, err := createRenderer(p, context)

				if err != nil {
					return err
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

				if _, ok := synthesizer.(otel.Synthesizer); !ok {
					synthesizer = otel.NewSynthesizer(p.Type, id, synthesizer)
				}

				cfg.RegisterSynthesizer(id, synthesizer)

			case ModelTypeTranscriber:
				transcriber, err := createTranscriber(p, context)

				if err != nil {
					return err
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

	if firstReranker != nil {
		cfg.reranker[""] = firstReranker
	}

	return nil
}

type providerConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

	MaxRetries *int `yaml:"max_retries"`

	Models yaml.Node `yaml:"models"`
}

func normalizeURL(url string, suffix string) string {
	url = strings.TrimRight(url, "/")
	url = strings.TrimSuffix(url, suffix)

	return url + suffix
}
