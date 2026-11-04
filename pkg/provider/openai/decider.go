package openai

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
)

var _ provider.Decider = (*Decider)(nil)

type Decider struct {
	*Config
	decisions openai.DecisionService
}

func NewDecider(url, model string, options ...Option) (*Decider, error) {
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("decision model is required")
	}
	cfg := &Config{url: url, model: model}
	for _, option := range options {
		option(cfg)
	}
	return &Decider{Config: cfg, decisions: openai.NewDecisionService(cfg.Options()...)}, nil
}

func (d *Decider) Decide(ctx context.Context, input *provider.DecisionInput) (*provider.Decision, error) {
	req, err := d.convertDecisionRequest(input)
	if err != nil {
		return nil, provider.InvalidRequest(err)
	}
	decision, err := d.decisions.New(ctx, *req)
	if err != nil {
		return nil, convertError(err)
	}
	return convertDecision(decision, input)
}
