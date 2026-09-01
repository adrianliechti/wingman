package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func (cfg *Config) RegisterRealtime(id string, p provider.Realtime) {
	cfg.RegisterModel(id)

	if cfg.realtime == nil {
		cfg.realtime = make(map[string]provider.Realtime)
	}

	if _, ok := cfg.realtime[""]; !ok {
		cfg.realtime[""] = p
	}

	cfg.realtime[id] = p
}

func (cfg *Config) Realtime(id string) (provider.Realtime, error) {
	if cfg.realtime != nil {
		if realtime, ok := cfg.realtime[id]; ok {
			return realtime, nil
		}
	}

	return nil, errors.New("realtime provider not found: " + id)
}

func createRealtime(cfg providerConfig, model modelContext) (provider.Realtime, error) {
	switch strings.ToLower(cfg.Type) {
	case "bedrock":
		var options []bedrock.Option

		if model.Client != nil {
			options = append(options, bedrock.WithClient(model.Client))
		}

		if region := cfg.Vars["region"]; region != "" {
			options = append(options, bedrock.WithRegion(region))
		}

		if voice := cfg.Vars["voice"]; voice != "" {
			options = append(options, bedrock.WithVoice(voice))
		}

		return bedrock.NewRealtime(model.ID, options...)

	case "gemini", "google":
		var options []google.Option
		if cfg.Token != "" {
			options = append(options, google.WithToken(cfg.Token))
		}
		if model.Client != nil {
			options = append(options, google.WithClient(model.Client))
		}
		if raw, ok := cfg.Vars["affective_dialog"]; ok {
			enabled, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, fmt.Errorf("google realtime: vars.affective_dialog must be a boolean: %w", err)
			}
			options = append(options, google.WithAffectiveDialog(enabled))
		}
		return google.NewRealtime(cfg.URL, model.ID, options...)

	case "openai", "openai-compatible":
		var options []openai.Option
		if cfg.Token != "" {
			options = append(options, openai.WithToken(cfg.Token))
		}
		if model.Client != nil {
			options = append(options, openai.WithClient(model.Client))
		}
		return openai.NewRealtime(cfg.URL, model.ID, options...)

	default:
		return nil, errors.New("invalid realtime provider type: " + cfg.Type)
	}
}
