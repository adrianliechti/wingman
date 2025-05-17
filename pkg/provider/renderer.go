package provider

import (
	"context"
)

type Renderer interface {
	Render(ctx context.Context, input string, options *RenderOptions) (*Rendering, error)
}

type RenderOptions struct {
	Images []File
}

type Rendering struct {
	ID    string
	Model string

	Content     []byte
	ContentType string
}
