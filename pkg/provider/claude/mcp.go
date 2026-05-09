package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// mcpServer is the in-process MCP server we expose to the CLI for caller-
// supplied tools (provider.CompleteOptions.Tools). The CLI sends JSON-RPC
// messages wrapped in `control_request { subtype: "mcp_message" }`; we route
// them here. Mirrors claude_agent_sdk._internal.query.Query._handle_sdk_mcp_request.
//
// We only advertise tools — we never execute them. tools/call returns an
// `isError: true` MCP result so the model recovers (or in practice, the
// preceding `tool_use` content block has already been emitted to the caller
// and that's what they wanted).
const (
	mcpServerName    = "wingman"
	mcpServerVersion = "1.0.0"
)

func toolPrefix() string {
	return "mcp__" + mcpServerName + "__"
}

// stripToolPrefix removes our MCP-server prefix from a tool name so it
// matches what the caller registered. Built-in CLI tools are not prefixed
// and pass through unchanged.
func stripToolPrefix(name string) string {
	if len(name) > len(toolPrefix()) && name[:len(toolPrefix())] == toolPrefix() {
		return name[len(toolPrefix()):]
	}
	return name
}

type mcpServer struct {
	tools map[string]provider.Tool
}

func newMcpServer(tools []provider.Tool) *mcpServer {
	m := &mcpServer{tools: make(map[string]provider.Tool, len(tools))}
	for _, t := range tools {
		m.tools[t.Name] = t
	}
	return m
}

func (m *mcpServer) dispatch(ctx context.Context, raw json.RawMessage) jsonrpcResponse {
	var req jsonrpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return jsonrpcResponse{
			JSONRPC: "2.0",
			Error:   &jsonrpcError{Code: -32700, Message: "parse error: " + err.Error()},
		}
	}

	switch req.Method {
	case "initialize":
		return jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{
					"name":    mcpServerName,
					"version": mcpServerVersion,
				},
			},
		}

	case "notifications/initialized":
		return jsonrpcResponse{JSONRPC: "2.0", Result: map[string]any{}}

	case "tools/list":
		names := make([]string, 0, len(m.tools))
		for name := range m.tools {
			names = append(names, name)
		}
		sort.Strings(names)

		tools := make([]map[string]any, 0, len(names))
		for _, name := range names {
			t := m.tools[name]
			schema := t.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": schema,
			})
		}
		return jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": tools},
		}

	case "tools/call":
		var params struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &params)
		return jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": fmt.Sprintf("tool %q is host-handled; result will be supplied by the caller", params.Name)},
				},
				"isError": true,
			},
		}

	default:
		return jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonrpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}
