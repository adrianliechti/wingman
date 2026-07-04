package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/obo"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	_ tool.Provider = (*Client)(nil)
	_ tool.Resulter = (*Client)(nil)
)

type Client struct {
	transport mcp.Transport
}

func New(url string, headers map[string]string, exchanger *obo.Exchanger) (*Client, error) {
	hc := &http.Client{
		Transport: &rt{
			headers:   headers,
			exchanger: exchanger,
			transport: http.DefaultTransport,
		},
	}

	var tr mcp.Transport = &mcp.StreamableClientTransport{
		Endpoint: url,

		HTTPClient: hc,
		MaxRetries: -1,
	}

	if strings.Contains(strings.ToLower(url), "/sse") {
		tr = &mcp.SSEClientTransport{
			Endpoint: url,

			HTTPClient: hc,
		}
	}

	c := &Client{
		transport: tr,
	}

	return c, nil
}

func (c *Client) createSession(ctx context.Context) (*mcp.ClientSession, error) {
	impl := &mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}

	opts := &mcp.ClientOptions{
		KeepAlive: time.Second * 30,
	}

	client := mcp.NewClient(impl, opts)
	return client.Connect(ctx, c.transport, nil)
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
		input, _ := t.InputSchema.(map[string]any)

		result = append(result, tool.Tool{
			Name:        t.Name,
			Description: t.Description,

			Parameters: tool.NormalizeSchema(input),
		})
	}

	return result, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	session, err := c.createSession(ctx)

	if err != nil {
		return nil, err
	}

	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: parameters,
	})

	if err != nil {
		return nil, err
	}

	if result.IsError {
		text := resultText(result)

		if text == "" {
			text = "tool execution failed"
		}

		return nil, errors.New(text)
	}

	return result, nil
}

// Result implements tool.Resulter so the model sees the MCP content parts
// (text, images, embedded resources) instead of the JSON-encoded SDK struct.
func (c *Client) Result(name string, value any) provider.ToolResult {
	result, ok := value.(*mcp.CallToolResult)
	if !ok {
		data, _ := json.Marshal(value)
		return provider.ToolResult{Parts: []provider.Part{{Text: string(data)}}}
	}

	var parts []provider.Part

	for _, content := range result.Content {
		switch v := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, provider.Part{Text: v.Text})

		case *mcp.ImageContent:
			parts = append(parts, filePart("", v.MIMEType, v.Data))

		case *mcp.AudioContent:
			parts = append(parts, filePart("", v.MIMEType, v.Data))

		case *mcp.EmbeddedResource:
			if v.Resource == nil {
				continue
			}

			if v.Resource.Text != "" {
				parts = append(parts, provider.Part{Text: v.Resource.Text})
				continue
			}

			if len(v.Resource.Blob) > 0 {
				parts = append(parts, filePart(v.Resource.URI, v.Resource.MIMEType, v.Resource.Blob))
			}

		case *mcp.ResourceLink:
			parts = append(parts, provider.Part{Text: "Resource: " + v.URI})
		}
	}

	if len(parts) == 0 && result.StructuredContent != nil {
		data, _ := json.Marshal(result.StructuredContent)
		parts = append(parts, provider.Part{Text: string(data)})
	}

	if len(parts) == 0 {
		parts = append(parts, provider.Part{Text: "(no content)"})
	}

	return provider.ToolResult{Parts: parts}
}

// filePart wraps binary content the completers can forward to the model
// (images, PDFs); other media becomes a text placeholder, since providers
// reject unsupported content types for the whole request.
func filePart(name, mimeType string, data []byte) provider.Part {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf":
		return provider.Part{File: &provider.File{Name: name, Content: data, ContentType: mimeType}}
	}

	if mimeType == "" {
		mimeType = "unknown type"
	}

	label := mimeType
	if name != "" {
		label = name + ", " + mimeType
	}

	return provider.Part{Text: fmt.Sprintf("[unsupported binary content: %s, %d bytes]", label, len(data))}
}

func resultText(result *mcp.CallToolResult) string {
	var parts []string

	for _, content := range result.Content {
		if v, ok := content.(*mcp.TextContent); ok && v.Text != "" {
			parts = append(parts, v.Text)
		}
	}

	return strings.Join(parts, "\n")
}

type rt struct {
	headers   map[string]string
	exchanger *obo.Exchanger
	transport http.RoundTripper
}

func (rt *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.exchanger != nil {
		if token, _ := req.Context().Value(auth.TokenContextKey).(string); token != "" {
			downstream, err := rt.exchanger.Token(req.Context(), token)

			if err != nil {
				return nil, err
			}

			req.Header.Set("Authorization", "Bearer "+downstream)
		}
	}

	for key, value := range rt.headers {
		if req.Header.Get(key) != "" {
			continue // already set
		}

		req.Header.Set(key, value)
	}

	return rt.transport.RoundTrip(req)
}
