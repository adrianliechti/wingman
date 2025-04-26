package limiter

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"

	"golang.org/x/time/rate"
)

type Renderer interface {
	Limiter
	provider.Renderer
}

type limitedRenderer struct {
	limiter  *rate.Limiter
	provider provider.Renderer
}

func NewRenderer(l *rate.Limiter, p provider.Renderer) Renderer {
	return &limitedRenderer{
		limiter:  l,
		provider: p,
	}
}

func (p *limitedRenderer) limiterSetup() {
}

func (p *limitedRenderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if p.limiter != nil {
		p.limiter.Wait(ctx)
	}

	return p.provider.Render(ctx, input, options)
}
