package realtime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNoStoreSessionForcesTracingOff(t *testing.T) {
	config := newSessionConfig(newFakeRealtime().Defaults())
	if err := config.apply(json.RawMessage(`{"type":"realtime","tracing":null}`)); err != nil {
		t.Fatalf("null tracing rejected: %v", err)
	}
	if got := config.object("sess_1", "model")["tracing"]; got != nil {
		t.Fatalf("session tracing = %#v, want null", got)
	}
	if err := config.apply(json.RawMessage(`{"type":"realtime","tracing":"auto"}`)); err == nil {
		t.Fatal("enabled tracing was accepted")
	}
}

func TestNoStorePolicyIsEnforcedOnWire(t *testing.T) {
	upstream := newFakeRealtime()
	conn := openTestRealtime(t, upstream)
	assertEventType(t, readWireEvent(t, conn), "session.created")
	assertEventType(t, readWireEvent(t, conn), "conversation.created")

	writeWireEvent(t, conn, map[string]any{
		"event_id": "trace_attempt", "type": "session.update",
		"session": map[string]any{"type": "realtime", "tracing": "auto"},
	})
	rejected := readWireEvent(t, conn)
	assertEventType(t, rejected, "error")
	apiError := rejected["error"].(map[string]any)
	if apiError["code"] != "invalid_request" || apiError["event_id"] != "trace_attempt" {
		t.Fatalf("tracing error = %#v", apiError)
	}
	if message, _ := apiError["message"].(string); !strings.Contains(message, "tracing must be null") {
		t.Fatalf("tracing error message = %q", message)
	}

	writeWireEvent(t, conn, map[string]any{
		"event_id": "mcp_attempt", "type": "session.update",
		"session": map[string]any{
			"type":  "realtime",
			"tools": []map[string]any{{"type": "mcp", "server_url": "https://example.com"}},
		},
	})
	rejected = readWireEvent(t, conn)
	assertEventType(t, rejected, "error")
	apiError = rejected["error"].(map[string]any)
	if apiError["code"] != "invalid_request" || apiError["event_id"] != "mcp_attempt" {
		t.Fatalf("MCP error = %#v", apiError)
	}

	writeWireEvent(t, conn, map[string]any{
		"type": "session.update", "session": map[string]any{"type": "realtime", "tracing": nil},
	})
	updated := readWireEvent(t, conn)
	assertEventType(t, updated, "session.updated")
	session := updated["session"].(map[string]any)
	if tracing, ok := session["tracing"]; !ok || tracing != nil {
		t.Fatalf("session tracing = %#v, want explicit null", tracing)
	}
}
