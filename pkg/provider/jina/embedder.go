package jina

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ provider.Embedder = (*Embedder)(nil)

type Embedder struct {
	*Config
}

func NewEmbedder(url string, model string, options ...Option) (*Embedder, error) {
	if url == "" {
		url = "https://api.jina.ai"
	}

	if model == "" {
		model = "jina-embeddings-v3"
	}

	url = strings.TrimRight(url, "/")
	url = strings.TrimSuffix(url, "/v1")

	cfg := &Config{
		url:   url,
		model: model,

		client: http.DefaultClient,
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
		"input": texts,
	}

	u, _ := url.JoinPath(e.url, "/v1/embeddings")

	req, _ := http.NewRequestWithContext(ctx, "POST", u, jsonReader(body))
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

	var embedding EmbeddingList

	if err := json.NewDecoder(resp.Body).Decode(&embedding); err != nil {
		return nil, err
	}

	result := &provider.Embedding{
		Model: e.model,
	}

	for _, e := range embedding.Data {
		result.Embeddings = append(result.Embeddings, e.Embedding)
	}

	return result, nil
}

type EmbeddingList struct {
	Object string `json:"object"`

	Model string      `json:"model"`
	Data  []Embedding `json:"data"`
}

type Embedding struct {
	Object string `json:"object"`

	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}
