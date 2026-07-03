package custom

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/tool"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"gopkg.in/yaml.v3"
)

var (
	_ tool.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client ToolClient
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" || !strings.HasPrefix(url, "grpc://") {
		return nil, errors.New("invalid url")
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

	var tools []tool.Tool

	for _, d := range resp.GetDefinitions() {
		tools = append(tools, tool.Tool{
			Name:        d.Name,
			Description: d.Description,

			Parameters: tool.ParseNormalizedSchema([]byte(d.Parameters)),
		})
	}

	return tools, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
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

	data := resp.GetData()

	var object map[string]any

	if err := json.Unmarshal([]byte(data), &object); err == nil {
		return object, nil
	}

	var list []any

	if err := json.Unmarshal([]byte(data), &list); err == nil {
		return list, nil
	}

	var document map[string]any

	if err := yaml.Unmarshal([]byte(data), &document); err == nil {
		return document, nil
	}

	return data, nil
}
