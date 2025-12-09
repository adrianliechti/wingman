package tool

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type File = provider.File
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

func WithFiles(ctx context.Context, files []File) context.Context {
	return context.WithValue(ctx, KeyToolFiles, files)
}

func FilesFromContext(ctx context.Context) ([]File, bool) {
	val := ctx.Value(KeyToolFiles)

	if val == nil {
		return nil, false
	}

	files, ok := val.([]File)
	return files, ok
}

func NormalizeSchema(schema map[string]any) map[string]any {
	// Handle empty schema
	if len(schema) == 0 {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	// Infer type from structure if missing
	if schema["type"] == nil {
		if schema["properties"] != nil {
			// Has properties -> should be object
			schema["type"] = "object"
		} else if schema["items"] != nil {
			// Has items -> should be array
			schema["type"] = "array"
		} else {
			// Default to object for function parameters
			schema["type"] = "object"
		}
	}

	// Ensure required fields exist based on type
	schemaType, _ := schema["type"].(string)
	switch schemaType {
	case "object":
		// Object type must have properties
		if schema["properties"] == nil {
			schema["properties"] = map[string]any{}
		}
	case "array":
		// Array type must have items
		if schema["items"] == nil {
			// Default to string items
			schema["items"] = map[string]any{"type": "string"}
		}
	}

	return schema
}

func ParseNormalizedSchema(data []byte) map[string]any {
	var schema map[string]any

	if len(data) == 0 || json.Unmarshal(data, &schema) != nil {
		schema = map[string]any{}
	}

	return NormalizeSchema(schema)
}
