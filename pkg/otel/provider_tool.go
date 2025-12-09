package otel

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/tool"

	"go.opentelemetry.io/otel"
)

type Tool interface {
	Observable
	tool.Provider
}

type observableTool struct {
	provider string

	tool tool.Provider
}

func NewTool(provider string, p tool.Provider) Tool {
	return &observableTool{
		tool: p,

		provider: provider,
	}
}

func (p *observableTool) otelSetup() {
}

func (p *observableTool) Tools(ctx context.Context) ([]tool.Tool, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "tools")
	defer span.End()

	tools, err := p.tool.Tools(ctx)

	return tools, err
}

func (p *observableTool) Execute(ctx context.Context, tool string, parameters map[string]any) (any, error) {
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "execute_tool "+tool)
	defer span.End()

	result, err := p.tool.Execute(ctx, tool, parameters)

	return result, err
}
