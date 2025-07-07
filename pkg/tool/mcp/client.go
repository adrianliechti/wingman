package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"

	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ tool.Provider = (*Client)(nil)

type Client struct {
	transportFn func() (mcp.Transport, error)
}

func NewCommand(command string, env, args []string) (*Client, error) {
	return &Client{
		transportFn: func() (mcp.Transport, error) {
			cmd := exec.Command(command, args...)
			return mcp.NewCommandTransport(cmd), nil
		},
	}, nil
}

func NewStreamable(url string, headers map[string]string) (*Client, error) {
	var client *http.Client

	if len(headers) > 0 {
		client = &http.Client{
			Transport: &rt{
				headers: headers,
			},
		}
	}

	return &Client{
		transportFn: func() (mcp.Transport, error) {
			return mcp.NewStreamableClientTransport(url, &mcp.StreamableClientTransportOptions{
				HTTPClient: client,
			}), nil
		},
	}, nil
}

func NewSSE(url string, headers map[string]string) (*Client, error) {
	var client *http.Client

	if len(headers) > 0 {
		client = &http.Client{
			Transport: &rt{
				headers: headers,
			},
		}
	}

	return &Client{
		transportFn: func() (mcp.Transport, error) {
			return mcp.NewSSEClientTransport(url, &mcp.SSEClientTransportOptions{
				HTTPClient: client,
			}), nil
		},
	}, nil
}

func (c *Client) createSession(ctx context.Context) (*mcp.ClientSession, error) {
	transport, err := c.transportFn()

	if err != nil {
		return nil, err
	}

	client := mcp.NewClient("wingman", "1.0.0", nil)
	return client.Connect(ctx, transport)
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	session, err := c.createSession(ctx)

	if err != nil {
		return nil, err
	}

	defer session.Close()

	resp, err := session.ListTools(ctx, nil)

	if err != nil {
		return nil, err
	}

	var result []tool.Tool

	for _, t := range resp.Tools {
		var schema map[string]any

		input, _ := t.InputSchema.MarshalJSON()

		if err := json.Unmarshal([]byte(input), &schema); err != nil {
			return nil, err
		}

		if len(t.InputSchema.Properties) == 0 {
			schema = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dummy_property": map[string]any{
						"type": "null",
					},
				},
			}
		}

		tool := tool.Tool{
			Name:        t.Name,
			Description: t.Description,

			Parameters: schema,
		}

		result = append(result, tool)
	}

	return result, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	session, err := c.createSession(ctx)

	if err != nil {
		return nil, err
	}

	defer session.Close()

	resp, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: parameters,
	})

	if err != nil {
		return nil, err
	}

	if len(resp.Content) > 1 {
		return nil, errors.New("multiple content types not supported")
	}

	for _, content := range resp.Content {
		switch content := content.(type) {
		case *mcp.TextContent:
			text := strings.TrimSpace(content.Text)
			return text, nil
		case *mcp.ImageContent:
			return nil, errors.New("image content not supported")
		case *mcp.AudioContent:
			return nil, errors.New("audio content not supported")
		case *mcp.EmbeddedResource:
			return nil, errors.New("embedded resource not supported")
		default:
			return nil, errors.New("unknown content type")
		}

	}

	return nil, errors.New("no content returned")
}

type rt struct {
	headers   map[string]string
	transport http.RoundTripper
}

func (rt *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	for key, value := range rt.headers {
		if req.Header.Get(key) != "" {
			continue // already set
		}

		req.Header.Set(key, value)
	}

	tr := rt.transport

	if tr == nil {
		tr = http.DefaultTransport
	}

	return tr.RoundTrip(req)
}
