package limiter

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/translator"

	"golang.org/x/time/rate"
)

type Translator interface {
	Limiter
	translator.Provider
}

type limitedTranslator struct {
	limiter  *rate.Limiter
	provider translator.Provider
}

func NewTranslator(l *rate.Limiter, p translator.Provider) Translator {
	return &limitedTranslator{
		limiter:  l,
		provider: p,
	}
}

func (p *limitedTranslator) limiterSetup() {
}

func (p *limitedTranslator) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	if p.limiter != nil {
		p.limiter.Wait(ctx)
	}

	return p.provider.Translate(ctx, input, options)
}
