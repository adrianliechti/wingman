package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server/openai"
)

type ModelService struct {
	Options []RequestOption
}

func NewModelService(opts ...RequestOption) ModelService {
	return ModelService{
		Options: opts,
	}
}

type Model = provider.Model

func (r *ModelService) List(ctx context.Context, opts ...RequestOption) ([]Model, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	req, _ := http.NewRequestWithContext(ctx, "GET", c.URL+"/v1/models", nil)
	req.Header.Set("Content-Type", "application/json")

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	var result openai.ModelList

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var models []provider.Model

	for _, m := range result.Models {
		models = append(models, provider.Model{
			ID: m.ID,
		})
	}

	return models, nil
}
