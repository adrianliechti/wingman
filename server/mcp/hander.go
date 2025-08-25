package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/config"

	"github.com/go-chi/chi/v5"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Handler struct {
	*config.Config
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		Config: cfg,
	}
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
	impl := &mcp.Implementation{
		Name:    "wingman",
		Version: "0.1.0",
	}

	opts := &mcp.ServerOptions{
		KeepAlive: time.Second * 30,
	}

	server := mcp.NewServer(impl, opts)

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

			handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args map[string]any

				if r, ok := req.Params.Arguments.(json.RawMessage); ok {
					json.Unmarshal(r, &args)
				}

				result, err := p.Execute(ctx, t.Name, args)

				if err != nil {
					return nil, err
				}

				switch v := result.(type) {
				case *mcp.CallToolResult:
					return v, nil

				case string:
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{
								Text: v,
							},
						},
					}, nil

				default:
					data, _ := json.Marshal(v)

					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{
								Text: string(data),
							},
						},
					}, nil
				}
			}

			tool := &mcp.Tool{
				Name:        t.Name,
				Description: t.Description,

				InputSchema: schema,
			}

			server.AddTool(tool, handler)
		}
	}

	return server, nil
}
