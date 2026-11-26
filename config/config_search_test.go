package config

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/searcher/keenable"
)

func TestCreateKeenableSearcherWithoutToken(t *testing.T) {
	for _, cfg := range []searcherConfig{
		{Type: "keenable"},
		{Type: "Keenable", Token: "key", Vars: map[string]string{"title": "my-app"}},
	} {
		p, err := createSearcher(cfg, searcherContext{})
		if err != nil {
			t.Fatalf("%+v: %v", cfg, err)
		}
		if _, ok := p.(*keenable.Client); !ok {
			t.Fatalf("%+v: got %T", cfg, p)
		}
	}
}
