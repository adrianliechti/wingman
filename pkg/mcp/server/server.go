package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	mcppkg "github.com/adrianliechti/wingman/pkg/mcp"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ mcppkg.Provider = (*Server)(nil)

type Server struct {
	http.Handler

	tools []tool.Provider

	server *mcp.Server
}

func New(name string, tools []tool.Provider) (*Server, error) {
	serverImpl := &mcp.Implementation{
		Name: name,
	}

	serverOpts := &mcp.ServerOptions{
		KeepAlive: time.Second * 30,
	}

	server := mcp.NewServer(serverImpl, serverOpts)

	handlerOpts := &mcp.StreamableHTTPOptions{
		Stateless: true,
	}

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, handlerOpts)

	s := &Server{
		Handler: handler,

		server: server,
		tools:  tools,
	}

	go s.refresh()

	return s, nil
}

func (s *Server) refresh() {
	for {
		if err := s.refreshTools(); err != nil {
			time.Sleep(time.Second * 30)
			continue
		}

		time.Sleep(time.Minute * 5)
	}
}

func (s *Server) refreshTools() error {
	ctx := context.Background()

	var resultErrr error

	for _, p := range s.tools {
		tools, err := p.Tools(ctx)

		if err != nil {
			resultErrr = errors.Join(resultErrr, err)
			continue
		}

		for _, t := range tools {
			data, _ := json.Marshal(t.Parameters)

			schema := new(jsonschema.Schema)

			if err := schema.UnmarshalJSON(data); err != nil {
				resultErrr = errors.Join(resultErrr, err)
				continue
			}

			handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args map[string]any
				json.Unmarshal(req.Params.Arguments, &args)

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

			s.server.AddTool(tool, handler)
		}
	}

	return resultErrr
}
