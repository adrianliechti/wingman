package config

import (
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/azure"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/pkg/provider/cohere"
	"github.com/adrianliechti/wingman/pkg/provider/custom"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/groq"
	"github.com/adrianliechti/wingman/pkg/provider/huggingface"
	"github.com/adrianliechti/wingman/pkg/provider/llama"
	"github.com/adrianliechti/wingman/pkg/provider/mistral"
	"github.com/adrianliechti/wingman/pkg/provider/mistralrs"
	"github.com/adrianliechti/wingman/pkg/provider/ollama"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/provider/xai"
)

func (cfg *Config) RegisterCompleter(id string, p provider.Completer) {
	cfg.RegisterModel(id)

	if cfg.completer == nil {
		cfg.completer = make(map[string]provider.Completer)
	}

	if _, ok := cfg.completer[""]; !ok {
		cfg.completer[""] = p
	}

	cfg.completer[id] = p
}

func (cfg *Config) Completer(id string) (provider.Completer, error) {
	if cfg.completer != nil {
		if c, ok := cfg.completer[id]; ok {
			return c, nil
		}
	}

	if cfg.chains != nil {
		if c, ok := cfg.chains[id]; ok {
			return c, nil
		}
	}

	return nil, errors.New("completer not found: " + id)
}

func createCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	switch strings.ToLower(cfg.Type) {
	case "anthropic":
		return anthropicCompleter(cfg, model)

	case "azure":
		return azureCompleter(cfg, model)

	case "bedrock":
		return bedrockCompleter(cfg, model)

	case "cohere":
		return cohereCompleter(cfg, model)

	case "github":
		return azureCompleter(cfg, model)

	case "google":
		return googleCompleter(cfg, model)

	case "groq":
		return groqCompleter(cfg, model)

	case "huggingface":
		return huggingfaceCompleter(cfg, model)

	case "llama":
		return llamaCompleter(cfg, model)

	case "mistral":
		return mistralCompleter(cfg, model)

	case "mistralrs":
		return mistralrsCompleter(cfg, model)

	case "ollama":
		return ollamaCompleter(cfg, model)

	case "openai":
		return openaiCompleter(cfg, model)

	case "xai":
		return xaiCompleter(cfg, model)

	case "custom":
		return customCompleter(cfg, model)

	default:
		return nil, errors.New("invalid completer type: " + cfg.Type)
	}
}

func anthropicCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []anthropic.Option

	if cfg.Token != "" {
		options = append(options, anthropic.WithToken(cfg.Token))
	}

	return anthropic.NewCompleter(cfg.URL, model.ID, options...)
}

func azureCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []azure.Option

	if cfg.Token != "" {
		options = append(options, azure.WithToken(cfg.Token))
	}

	return azure.NewCompleter(cfg.URL, model.ID, options...)
}

func bedrockCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []bedrock.Option

	return bedrock.NewCompleter(model.ID, options...)
}

func cohereCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []cohere.Option

	if cfg.Token != "" {
		options = append(options, cohere.WithToken(cfg.Token))
	}

	return cohere.NewCompleter(model.ID, options...)
}

func googleCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []google.Option

	if cfg.Token != "" {
		options = append(options, google.WithToken(cfg.Token))
	}

	return google.NewCompleter(model.ID, options...)
}

func groqCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []groq.Option

	if cfg.Token != "" {
		options = append(options, groq.WithToken(cfg.Token))
	}

	return groq.NewCompleter(model.ID, options...)
}

func huggingfaceCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []huggingface.Option

	if cfg.Token != "" {
		options = append(options, huggingface.WithToken(cfg.Token))
	}

	return huggingface.NewCompleter(cfg.URL, model.ID, options...)
}

func llamaCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []llama.Option

	return llama.NewCompleter(model.ID, cfg.URL, options...)
}

func mistralCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []mistral.Option

	if cfg.Token != "" {
		options = append(options, mistral.WithToken(cfg.Token))
	}

	return mistral.NewCompleter(model.ID, options...)
}

func mistralrsCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []mistralrs.Option

	return mistralrs.NewCompleter(cfg.URL, model.ID, options...)
}

func ollamaCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []ollama.Option

	return ollama.NewCompleter(cfg.URL, model.ID, options...)
}

func openaiCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []openai.Option

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	return openai.NewCompleter(cfg.URL, model.ID, options...)
}

func xaiCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []xai.Option

	if cfg.Token != "" {
		options = append(options, xai.WithToken(cfg.Token))
	}

	return xai.NewCompleter(cfg.URL, model.ID, options...)
}

func customCompleter(cfg providerConfig, model modelContext) (provider.Completer, error) {
	var options []custom.Option

	return custom.NewCompleter(cfg.URL, options...)
}
