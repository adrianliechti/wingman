package huggingface

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Embedder = (*Embedder)(nil)

type Embedder struct {
	*Config
}

func NewEmbedder(url, model string, options ...Option) (*Embedder, error) {
	if url == "" {
		url = "https://api-inference.huggingface.co/pipeline/feature-extraction/" + model
	}

	url = strings.TrimRight(url, "/")

	cfg := &Config{
		client: http.DefaultClient,

		url:   url,
		token: "-",

		model: "tei",
	}

	for _, option := range options {
		option(cfg)
	}

	return &Embedder{
		Config: cfg,
	}, nil
}

func (e *Embedder) Embed(ctx context.Context, texts []string) (*provider.Embedding, error) {
	body := map[string]any{
		"inputs": texts,
	}

	url := e.url + "/embed"

	if strings.Contains(e.url, "api-inference.huggingface.co") {
		url = e.url
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", url, jsonReader(body))
	req.Header.Set("Content-Type", "application/json")

	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}

	resp, err := e.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	data, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	var result1 []float32

	if err := json.Unmarshal(data, &result1); err == nil {
		return &provider.Embedding{
			Model: e.model,

			Embeddings: [][]float32{result1},
		}, nil
	}

	var result2 [][]float32

	if err := json.Unmarshal(data, &result2); err == nil && len(result2) > 0 {
		return &provider.Embedding{
			Model: e.model,

			Embeddings: result2,
		}, nil
	}

	return nil, errors.New("unable to embed input")
}
