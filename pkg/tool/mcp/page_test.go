package mcp

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolsFollowsPagination guards against silently dropping tools past the
// upstream server's page size, which a single ListTools call would do.
func TestToolsFollowsPagination(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "paged", Version: "1.0.0"}, &mcp.ServerOptions{
		PageSize: 2,
	})

	var want []string

	for i := range 5 {
		name := fmt.Sprintf("tool_%d", i)
		want = append(want, name)

		mcp.AddTool(server, &mcp.Tool{Name: name, Description: "d"},
			func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{}, nil, nil
			})
	}

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	c, err := newHTTPTestClient(t, handler, nil, nil)

	if err != nil {
		t.Fatal(err)
	}

	tools, err := c.Tools(t.Context())

	if err != nil {
		t.Fatal(err)
	}

	var got []string

	for _, tl := range tools {
		got = append(got, tl.Name)
	}

	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
}
