package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResult_ContentParts(t *testing.T) {
	c := &Client{}

	out := c.Result("tool", &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello"},
			&mcp.ImageContent{Data: []byte{1, 2, 3}, MIMEType: "image/png"},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///a.txt", Text: "embedded"}},
			&mcp.ResourceLink{URI: "https://example.com/doc"},
		},
	})

	if len(out.Parts) != 4 {
		t.Fatalf("parts = %d, want 4", len(out.Parts))
	}
	if out.Parts[0].Text != "hello" {
		t.Errorf("text part = %q", out.Parts[0].Text)
	}
	if out.Parts[1].File == nil || out.Parts[1].File.ContentType != "image/png" {
		t.Errorf("image part = %+v", out.Parts[1])
	}
	if out.Parts[2].Text != "embedded" {
		t.Errorf("resource part = %q", out.Parts[2].Text)
	}
	if out.Parts[3].Text != "Resource: https://example.com/doc" {
		t.Errorf("link part = %q", out.Parts[3].Text)
	}
}

func TestResult_StructuredContentAlongsideTextAndImages(t *testing.T) {
	c := &Client{}
	for _, content := range [][]mcp.Content{
		{&mcp.TextContent{Text: "Lookup completed"}},
		{&mcp.ImageContent{Data: []byte{1}, MIMEType: "image/png"}},
		{&mcp.TextContent{Text: ""}},
	} {
		out := c.Result("lookup", &mcp.CallToolResult{Content: content, StructuredContent: map[string]any{"answer": 42}})
		if out.Parts[len(out.Parts)-1].Text != `{"answer":42}` {
			t.Fatalf("structured data was hidden: %+v", out)
		}
	}
	for _, text := range []string{`{"answer":42,"source":"api"}`, " { \"source\": \"api\", \"answer\": 42 } "} {
		out := c.Result("lookup", &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: text}},
			StructuredContent: map[string]any{"answer": 42, "source": "api"},
		})
		if len(out.Parts) != 1 {
			t.Fatalf("duplicated structured content: %+v", out)
		}
	}
	if out := c.Result("lookup", (*mcp.CallToolResult)(nil)); out.Parts[0].Text != "(no content)" {
		t.Fatalf("nil result %+v", out)
	}
}

func TestExecute_HTTPResultsAndFailures(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "tools", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Retrieved"}}, StructuredContent: map[string]any{"answer": 42}}, nil
		})
	server.AddTool(&mcp.Tool{Name: "failure", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"error": "upstream unavailable"}}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	c, err := newHTTPTestClient(t, handler, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := c.Execute(t.Context(), "lookup", nil)
	if err != nil {
		t.Fatal(err)
	}
	result := c.Result("lookup", value)
	if len(result.Parts) != 2 || result.Parts[1].Text != `{"answer":42}` {
		t.Fatalf("result %+v", result)
	}
	if _, err := c.Execute(t.Context(), "failure", nil); err == nil || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("tool error %v", err)
	}
	if _, err := c.Execute(t.Context(), "unknown", nil); err == nil {
		t.Fatal("unknown tool succeeded")
	}
	if _, err := c.Execute(t.Context(), "", nil); err == nil {
		t.Fatal("blank tool name succeeded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Execute(ctx, "lookup", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
}

func TestResult_UnsupportedBinaryBecomesText(t *testing.T) {
	c := &Client{}

	out := c.Result("tool", &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.AudioContent{Data: []byte{1, 2, 3}, MIMEType: "audio/wav"},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///a.bin", MIMEType: "application/octet-stream", Blob: []byte{1, 2}}},
		},
	})

	if len(out.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(out.Parts))
	}
	if out.Parts[0].File != nil || out.Parts[0].Text != "[unsupported binary content: audio/wav, 3 bytes]" {
		t.Errorf("audio part = %+v", out.Parts[0])
	}
	if out.Parts[1].File != nil || out.Parts[1].Text != "[unsupported binary content: file:///a.bin, application/octet-stream, 2 bytes]" {
		t.Errorf("blob part = %+v", out.Parts[1])
	}
}

func TestResult_PDFResourcePassesThrough(t *testing.T) {
	c := &Client{}

	out := c.Result("tool", &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///a.pdf", MIMEType: "application/pdf", Blob: []byte{1}}},
		},
	})

	if len(out.Parts) != 1 || out.Parts[0].File == nil || out.Parts[0].File.ContentType != "application/pdf" {
		t.Errorf("got %+v", out.Parts)
	}
}

func TestResult_StructuredContentFallback(t *testing.T) {
	c := &Client{}

	out := c.Result("tool", &mcp.CallToolResult{
		StructuredContent: map[string]any{"answer": 42},
	})

	if len(out.Parts) != 1 || out.Parts[0].Text != `{"answer":42}` {
		t.Errorf("got %+v", out.Parts)
	}
}

func TestResult_EmptyContent(t *testing.T) {
	c := &Client{}

	out := c.Result("tool", &mcp.CallToolResult{})

	if len(out.Parts) != 1 || out.Parts[0].Text != "(no content)" {
		t.Errorf("got %+v", out.Parts)
	}
}

func TestResultText_JoinsTextParts(t *testing.T) {
	got := resultText(&mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "a"},
			&mcp.ImageContent{Data: []byte{1}},
			&mcp.TextContent{Text: "b"},
		},
	})

	if got != "a\nb" {
		t.Errorf("got %q", got)
	}
}
