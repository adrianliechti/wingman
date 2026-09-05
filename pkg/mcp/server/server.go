package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	http.Handler

	tools []tool.Provider

	server *mcp.Server

	mu sync.Mutex
	// registered maps each provider's advertised tool names to a revision of
	// their definition, so a refresh can retire tools that disappeared upstream
	// and skip re-adding ones that did not change.
	registered []map[string]string
}

func New(name, instructions string, tools []tool.Provider) (*Server, error) {
	serverImpl := &mcp.Implementation{
		Name:    name,
		Version: "1.0.0",
	}

	serverOpts := &mcp.ServerOptions{
		Instructions: instructions,

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

		registered: make([]map[string]string, len(tools)),
	}

	go s.refresh()

	return s, nil
}

func (s *Server) Icon() (string, []byte) {
	return "", nil
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

	var resultErr error

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.tools {
		tools, err := p.Tools(ctx)

		if err != nil {
			// Keep the previously registered tools: a transient upstream
			// failure should not retract them.
			resultErr = errors.Join(resultErr, err)
			continue
		}

		current := make(map[string]string, len(tools))

		for _, t := range tools {
			data, _ := json.Marshal(t.Parameters)

			schema := new(jsonschema.Schema)

			if err := schema.UnmarshalJSON(data); err != nil {
				resultErr = errors.Join(resultErr, err)
				continue
			}

			revision := t.Description + "\x00" + string(data)
			current[t.Name] = revision

			// AddTool unconditionally reports a change, so re-adding an
			// unchanged tool would notify every client on each refresh.
			if s.registered[i][t.Name] == revision {
				continue
			}

			handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args map[string]any

				if len(req.Params.Arguments) > 0 {
					if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
						return nil, err
					}
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

			s.server.AddTool(&mcp.Tool{
				Name:        t.Name,
				Description: t.Description,

				InputSchema: schema,
			}, handler)
		}

		var stale []string

		for name := range s.registered[i] {
			if _, ok := current[name]; !ok {
				stale = append(stale, name)
			}
		}

		if len(stale) > 0 {
			s.server.RemoveTools(stale...)
		}

		s.registered[i] = current
	}

	return resultErr
}
