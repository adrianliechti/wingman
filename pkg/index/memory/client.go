package memory

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/index"

	"github.com/google/uuid"
)

var _ index.Provider = &Provider{}

type Provider struct {
	embedder index.Embedder
	reranker index.Reranker

	documents map[string]index.Document
}

func New(options ...Option) (*Provider, error) {
	p := &Provider{
		documents: make(map[string]index.Document),
	}

	for _, option := range options {
		option(p)
	}

	if p.embedder == nil {
		return nil, errors.New("embedder is required")
	}

	return p, nil
}

func (p *Provider) List(ctx context.Context, options *index.ListOptions) (*index.Page[index.Document], error) {
	items := make([]index.Document, 0, len(p.documents))

	for _, d := range p.documents {
		items = append(items, d)
	}

	page := index.Page[index.Document]{
		Items: items,
	}

	return &page, nil
}

func (p *Provider) Index(ctx context.Context, documents ...index.Document) error {
	for _, d := range documents {
		if d.ID == "" {
			d.ID = uuid.NewString()
		}

		if len(d.Embedding) == 0 && p.embedder != nil {
			embedding, err := p.embedder.Embed(ctx, []string{d.Content})

			if err != nil {
				return err
			}

			d.Embedding = embedding.Embeddings[0]
		}

		if len(d.Embedding) == 0 {
			continue
		}

		p.documents[d.ID] = d
	}

	return nil
}

func (p *Provider) Delete(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		delete(p.documents, id)
	}

	return nil
}

func (p *Provider) Query(ctx context.Context, query string, options *index.QueryOptions) ([]index.Result, error) {
	if options == nil {
		options = &index.QueryOptions{}
	}

	if p.embedder == nil {
		return nil, errors.New("no embedder configured")
	}

	embedding, err := p.embedder.Embed(ctx, []string{query})

	if err != nil {
		return nil, err
	}

	results := make([]index.Result, 0)

DOCUMENTS:
	for _, d := range p.documents {
		for k, v := range options.Filters {
			val, ok := d.Metadata[k]

			if !ok {
				continue DOCUMENTS
			}

			if !strings.EqualFold(v, val) {
				continue DOCUMENTS
			}
		}

		score := cosineSimilarity(embedding.Embeddings[0], d.Embedding)

		r := index.Result{
			Score:    score,
			Document: d,
		}

		results = append(results, r)
	}

	slices.SortFunc(results, func(a, b index.Result) int {
		return cmp.Compare(b.Score, a.Score)
	})

	if options.Limit != nil {
		limit := min(*options.Limit, len(results))
		results = results[:limit]
	}

	return results, nil
}

func cosineSimilarity(vals1, vals2 []float32) float32 {
	l2norm := func(v float64, s, t float64) (float64, float64) {
		if v == 0 {
			return s, t
		}

		a := math.Abs(v)

		if a > t {
			r := t / v
			s = 1 + s*r*r
			t = a
		} else {
			r := v / t
			s = s + r*r
		}

		return s, t
	}

	dot := float64(0)

	s1 := float64(1)
	t1 := float64(0)

	s2 := float64(1)
	t2 := float64(0)

	for i, v1f := range vals1 {
		v1 := float64(v1f)
		v2 := float64(vals2[i])

		dot += v1 * v2

		s1, t1 = l2norm(v1, s1, t1)
		s2, t2 = l2norm(v2, s2, t2)
	}

	l1 := t1 * math.Sqrt(s1)
	l2 := t2 * math.Sqrt(s2)

	return float32(dot / (l1 * l2))
}
