package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

var _ tool.Provider = (*Client)(nil)

type Client struct {
	transportFn func() (transport.Interface, error)
}

func NewStdio(command string, env, args []string) (*Client, error) {
	return &Client{
		transportFn: func() (transport.Interface, error) {
			return transport.NewStdio(command, env, args...), nil
		},
	}, nil
}

func NewSSE(url string, headers map[string]string) (*Client, error) {
	return &Client{
		transportFn: func() (transport.Interface, error) {
			var options []transport.ClientOption

			if len(headers) > 0 {
				options = append(options, transport.WithHeaders(headers))
			}

			return transport.NewSSE(url, options...)
		},
	}, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	client, err := c.createClient(ctx)

	if err != nil {
		return nil, err
	}

	defer client.Close()

	req := mcp.ListToolsRequest{}

	resp, err := client.ListTools(ctx, req)

	if err != nil {
		return nil, err
	}

	var result []tool.Tool

	for _, t := range resp.Tools {
		var schema map[string]any

		input, _ := json.Marshal(t.InputSchema)

		if err := json.Unmarshal([]byte(input), &schema); err != nil {
			return nil, err
		}

		if len(t.InputSchema.Properties) == 0 {
			schema = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
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
	client, err := c.createClient(ctx)

	if err != nil {
		return nil, err
	}

	defer client.Close()

	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = parameters

	resp, err := client.CallTool(ctx, req)

	if err != nil {
		return nil, err
	}

	if len(resp.Content) > 1 {
		return nil, errors.New("multiple content types not supported")
	}

	for _, content := range resp.Content {
		switch content := content.(type) {
		case mcp.TextContent:
			text := strings.TrimSpace(content.Text)
			return text, nil

		case mcp.ImageContent:
			return nil, errors.New("image content not supported")

		case mcp.EmbeddedResource:
			return nil, errors.New("embedded resource not supported")

		default:
			return nil, errors.New("unknown content type")
		}
	}

	return nil, errors.New("no content returned")
}

func (c *Client) createClient(ctx context.Context) (*client.Client, error) {
	tr, err := c.transportFn()

	if err != nil {
		return nil, err
	}

	client := client.NewClient(tr)

	if err := client.Start(ctx); err != nil {
		client.Close()
		return nil, err
	}

	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}

	resp, err := client.Initialize(ctx, req)

	if err != nil {
		client.Close()
		return nil, err
	}

	_ = resp

	return client, nil
}
