package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	_ tool.Provider = (*Client)(nil)
	_ tool.Resulter = (*Client)(nil)
)

type Client struct {
	client    *http.Client
	transport mcp.Transport
}

func New(endpoint string, headers map[string]string, exchanger auth.TokenExchanger, options ...Option) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, errors.New("mcp: endpoint must be an absolute http(s) URL")
	}
	c := &Client{client: http.DefaultClient}
	for _, option := range options {
		option(c)
	}
	hc := *c.client
	transport := hc.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	hc.Transport = &rt{
		headers:   maps.Clone(headers),
		exchanger: exchanger,
		transport: transport,
	}

	var tr mcp.Transport = &mcp.StreamableClientTransport{
		Endpoint: endpoint,

		HTTPClient: &hc,
		MaxRetries: -1,
	}

	if strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/sse") {
		tr = &mcp.SSEClientTransport{
			Endpoint: endpoint,

			HTTPClient: &hc,
		}
	}

	c.transport = tr

	return c, nil
}

func (c *Client) createSession(ctx context.Context) (*mcp.ClientSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

	var result []tool.Tool

	// Tools paginates; ListTools would silently stop at the server's page size.
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}

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
	if strings.TrimSpace(name) == "" {
		return nil, tool.ErrInvalidTool
	}
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
	if result == nil {
		return nil, errors.New("mcp: empty tool response")
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
		rendered, err := tool.RenderResult(nil, name, value)
		if err != nil {
			rendered = tool.TextResult("Error: " + err.Error())
			rendered.IsError = true
		}
		return rendered
	}
	if result == nil {
		return tool.TextResult("(no content)")
	}

	var parts []provider.Part

	for _, content := range result.Content {
		switch v := content.(type) {
		case *mcp.TextContent:
			if v.Text != "" {
				parts = append(parts, provider.Part{Text: v.Text})
			}

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

	if result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			failure := tool.TextResult("Error: invalid structured tool result: " + err.Error())
			failure.IsError = true
			return failure
		}
		if !containsStructuredContent(parts, data) {
			parts = append(parts, provider.Part{Text: string(data)})
		}
	}

	if len(parts) == 0 {
		parts = append(parts, provider.Part{Text: "(no content)"})
	}

	return provider.ToolResult{Parts: parts, IsError: result.IsError}
}

func containsStructuredContent(parts []provider.Part, data []byte) bool {
	for _, part := range parts {
		var value any
		if json.Unmarshal([]byte(part.Text), &value) == nil {
			encoded, _ := json.Marshal(value)
			if bytes.Equal(encoded, data) {
				return true
			}
		}
	}
	return false
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

	if len(parts) == 0 && result.StructuredContent != nil {
		data, _ := json.Marshal(result.StructuredContent)
		return string(data)
	}
	return strings.Join(parts, "\n")
}

type rt struct {
	headers   map[string]string
	exchanger auth.TokenExchanger
	transport http.RoundTripper
}

func (rt *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if rt.exchanger != nil {
		caller, _ := req.Context().Value(auth.TokenContextKey).(string)

		downstream, err := rt.exchanger.Token(req.Context(), caller)

		if err != nil {
			return nil, err
		}

		if downstream == "" {
			req.Header.Del("Authorization")
		} else {
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
