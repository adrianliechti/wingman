package multi

import (
	"cmp"
	"context"
	"slices"

	"github.com/adrianliechti/wingman/pkg/guard"
)

var _ guard.Provider = &Guard{}

type Guard struct {
	providers []guard.Provider
}

func New(provider ...guard.Provider) *Guard {
	return &Guard{
		providers: provider,
	}
}

func (g *Guard) Check(ctx context.Context, text string, options *guard.CheckOptions) (*guard.Result, error) {
	if options == nil {
		options = new(guard.CheckOptions)
	}

	result := &guard.Result{}

	scores := map[string]float64{}

	for _, p := range g.providers {
		r, err := p.Check(ctx, text, options)

		if err != nil {
			return nil, err
		}

		if r.Flagged {
			result.Flagged = true
		}

		for _, c := range r.Categories {
			if score, ok := scores[c.Name]; !ok || c.Score > score {
				scores[c.Name] = c.Score
			}
		}
	}

	for name, score := range scores {
		result.Categories = append(result.Categories, guard.Category{
			Name:  name,
			Score: score,
		})
	}

	slices.SortFunc(result.Categories, func(i, j guard.Category) int {
		return cmp.Compare(j.Score, i.Score)
	})

	return result, nil
}
