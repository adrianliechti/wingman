package mcp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/config"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Handler struct {
	*config.Config
	http.Handler
}

func New(cfg *config.Config) (*Handler, error) {
	mux := chi.NewMux()

	h := &Handler{
		Config:  cfg,
		Handler: mux,
	}

	h.Attach(mux)
	return h, nil
}

func (h *Handler) Attach(r chi.Router) {
	var server *mcp.Server

	getServer := func(request *http.Request) *mcp.Server {
		if server == nil {
			server, _ = h.createServer()
		}

		return server
	}

	r.Handle("/mcp", mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{}))
}

func (h *Handler) createServer() (*mcp.Server, error) {
	server := mcp.NewServer("wingman", "v0.1.0", nil)

	for _, p := range h.Tools() {
		tool, err := p.Tools(context.Background())

		if err != nil {
			return nil, err
		}

		for _, t := range tool {
			data, _ := json.Marshal(t.Parameters)

			schema := new(jsonschema.Schema)

			if err := schema.UnmarshalJSON(data); err != nil {
				return nil, err
			}

			handler := func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[any]) (*mcp.CallToolResultFor[any], error) {
				args, err := convertArgs(params.Arguments)

				if err != nil {
					return nil, err
				}

				result, err := p.Execute(ctx, t.Name, args)

				if err != nil {
					return nil, err
				}

				var content string

				switch v := result.(type) {
				case string:
					content = v
				default:
					data, _ := json.Marshal(v)
					content = string(data)
				}

				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: content,
						},
					},
				}, nil
			}

			tool := mcp.NewServerTool(t.Name, t.Description, handler, mcp.Input(mcp.Schema(schema)))

			server.AddTools(tool)
		}
	}

	return server, nil
}

func convertArgs(val any) (map[string]any, error) {
	data, err := json.Marshal(val)

	if err != nil {
		return nil, err
	}

	var args map[string]any

	if err := json.Unmarshal(data, &args); err == nil {
		return args, nil
	}

	return map[string]any{
		"input": val,
	}, nil
}
