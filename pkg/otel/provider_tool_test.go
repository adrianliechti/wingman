package otel

import (
	"context"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/searcher/custom"
	"github.com/adrianliechti/wingman/pkg/searcher/duckduckgo"
	"github.com/adrianliechti/wingman/pkg/searcher/exa"
	"github.com/adrianliechti/wingman/pkg/searcher/tavily"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/tool/mcp"
	"github.com/adrianliechti/wingman/pkg/tool/search"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

type plainTool struct{}

func (*plainTool) Tools(context.Context) ([]tool.Tool, error)                   { return nil, nil }
func (*plainTool) Execute(context.Context, string, map[string]any) (any, error) { return nil, nil }

func TestObservableTool_PreservesResultRendering(t *testing.T) {
	raw := &protocol.CallToolResult{Content: []protocol.Content{
		&protocol.TextContent{Text: "caption"},
		&protocol.ImageContent{Data: []byte{1, 2}, MIMEType: "image/png"},
	}, StructuredContent: map[string]any{"count": 42}}
	underlying := &mcp.Client{}
	want := underlying.Result("lookup", raw)
	wrapped := NewTool("mcp", underlying)
	got, err := tool.RenderResult(wrapped, "lookup", raw)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("lost MCP content: %+v, err %v", got, err)
	}
	plain := NewTool("custom", &plainTool{})
	got, err = tool.RenderResult(plain, "report", "Report\n[source](https://example.com)")
	if err != nil || !reflect.DeepEqual(got, tool.TextResult("Report\n[source](https://example.com)")) {
		t.Fatalf("quoted text %+v, err %v", got, err)
	}
	got, _ = tool.RenderResult(plain, "bad", make(chan int))
	if !got.IsError {
		t.Fatal("encoding error appeared successful")
	}
}

func TestObservableSearcher_PreservesCapabilitiesInToolSchema(t *testing.T) {
	for _, tt := range []struct {
		name     string
		provider searcher.Provider
	}{
		{"exa", &exa.Client{}},
		{"tavily", &tavily.Client{}},
		{"duckduckgo", &duckduckgo.Client{}},
		{"custom", &custom.Client{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := search.New(tt.provider)
			want, err := raw.Tools(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wrapped, _ := search.New(NewSearcher(tt.name, "", tt.provider))
			got, err := wrapped.Tools(t.Context())
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("tracing changed tool schema: got %+v, want %+v, error %v", got, want, err)
			}
		})
	}
}
