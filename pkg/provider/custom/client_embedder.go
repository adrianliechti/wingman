package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ provider.Embedder = (*Embedder)(nil)
)

type Embedder struct {
	*Config

	url string

	client EmbedderClient
}

func NewEmbedder(url string, options ...Option) (*Embedder, error) {
	if url == "" || !strings.HasPrefix(url, "grpc://") {
		return nil, errors.New("invalid url")
	}

	cfg := &Config{}

	for _, option := range options {
		option(cfg)
	}

	c := &Embedder{
		Config: cfg,

		url: url,
	}

	url = strings.TrimPrefix(c.url, "grpc://")

	conn, err := grpc.Dial(url,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)

	if err != nil {
		return nil, err
	}

	c.client = NewEmbedderClient(conn)

	return c, nil
}

func (e *Embedder) Embed(ctx context.Context, texts []string) (*provider.Embedding, error) {
	req := &EmbedRequest{
		Texts: texts,
	}

	resp, err := e.client.Embed(ctx, req)

	if err != nil {
		return nil, err
	}

	var embeddings [][]float32

	for _, e := range resp.Embeddings {
		embeddings = append(embeddings, e.Data)
	}

	result := &provider.Embedding{
		Model: resp.Model,

		Embeddings: embeddings,
	}

	if resp.Usage != nil {
		result.Usage = &provider.Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		}
	}

	return result, nil
}
