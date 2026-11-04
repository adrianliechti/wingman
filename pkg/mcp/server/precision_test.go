package server

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type numberTool struct{ args map[string]any }

func (p *numberTool) Tools(context.Context) ([]tool.Tool, error) {
	return []tool.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}}, nil
}
func (p *numberTool) Execute(_ context.Context, _ string, args map[string]any) (any, error) {
	p.args = args
	return args, nil
}

func TestToolExecutionPreservesNumbers(t *testing.T) {
	const args = `{"id":9007199254740993,"values":[1.234567890123456789,1e400]}`
	p := &numberTool{}
	s := newServer(t, "", p)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "lookup", Arguments: jsontext.Value(args)})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p.args, json.Deterministic(true))
	if err != nil || string(data) != args {
		t.Fatalf("arguments changed: %s (%v)", data, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected one text result, got %+v", result)
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text result, got %T", result.Content[0])
	}
	if content.Text != args {
		t.Fatalf("result changed: %s", content.Text)
	}
}
