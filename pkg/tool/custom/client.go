package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/tool"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"go.yaml.in/yaml/v4"
)

var (
	_ tool.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client ToolClient
}

func New(url string, options ...Option) (*Client, error) {
	url = strings.TrimSpace(url)
	if url == "" || !strings.HasPrefix(url, "grpc://") {
		return nil, errors.New("invalid url")
	}
	if strings.TrimSpace(strings.TrimPrefix(url, "grpc://")) == "" {
		return nil, errors.New("custom tool: missing gRPC target")
	}

	c := &Client{
		url: url,
	}

	for _, option := range options {
		option(c)
	}

	client, err := grpc.NewClient(strings.TrimPrefix(c.url, "grpc://"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)), // 100MB max receive message size
	)

	if err != nil {
		return nil, err
	}
	c.client = NewToolClient(client)

	return c, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	resp, err := c.client.Tools(ctx, &ToolsRequest{})

	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("custom tool: empty tools response")
	}

	var tools []tool.Tool

	for _, d := range resp.GetDefinitions() {
		if d == nil || strings.TrimSpace(d.Name) == "" {
			return nil, errors.New("custom tool: missing tool name")
		}
		var schema map[string]any
		if strings.TrimSpace(d.Parameters) != "" {
			if err := json.Unmarshal([]byte(d.Parameters), &schema); err != nil {
				return nil, fmt.Errorf("custom tool %s: invalid parameter schema: %w", d.Name, err)
			}
			if schema == nil {
				return nil, fmt.Errorf("custom tool %s: parameters must have an object schema", d.Name)
			}
		}
		schema = tool.NormalizeSchema(schema)
		if schema["type"] != "object" {
			return nil, fmt.Errorf("custom tool %s: parameters must have an object schema", d.Name)
		}
		tools = append(tools, tool.Tool{
			Name:        d.Name,
			Description: d.Description,

			Parameters: schema,
		})
	}

	return tools, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, tool.ErrInvalidTool
	}
	if parameters == nil {
		parameters = map[string]any{}
	}
	params, err := json.Marshal(parameters)

	if err != nil {
		return nil, err
	}
	resp, err := c.client.Execute(ctx, &ExecuteRequest{
		Name:       name,
		Parameters: string(params),
	})

	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("custom tool: empty execution response")
	}

	data := resp.GetData()

	var value any
	if err := json.Unmarshal([]byte(data), &value); err == nil {
		return value, nil
	}

	var document map[string]any

	if err := yaml.Unmarshal([]byte(data), &document); err == nil {
		return document, nil
	}

	return data, nil
}
