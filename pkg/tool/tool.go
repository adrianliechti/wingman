package tool

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type Tool = provider.Tool

var (
	ErrInvalidTool = errors.New("invalid tool")
)

type Provider interface {
	Tools(ctx context.Context) ([]Tool, error)
	Execute(ctx context.Context, name string, parameters map[string]any) (any, error)
}

var (
	KeyToolFiles = "tool_files"
)

func WithFiles(ctx context.Context, files []provider.File) context.Context {
	return context.WithValue(ctx, KeyToolFiles, files)
}

func FilesFromContext(ctx context.Context) ([]provider.File, bool) {
	val := ctx.Value(KeyToolFiles)

	if val == nil {
		return nil, false
	}

	files, ok := val.([]provider.File)
	return files, ok
}
