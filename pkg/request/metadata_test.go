package request

import (
	"context"
	"net/http"
	"testing"
)

func TestFromHeaderNormalizesCodexMetadata(t *testing.T) {
	header := http.Header{}
	header.Set("session-id", "session-1")
	header.Set("thread-id", "agent-2")
	header.Set("x-codex-parent-thread-id", "agent-1")
	header.Set("x-openai-subagent", "collab_spawn")
	header.Set("x-codex-turn-metadata", `{"session_id":"session-1","thread_id":"agent-2","parent_thread_id":"agent-1","agent_name":"explorer","turn_id":"turn-4","subagent_kind":"collab_spawn"}`)
	header.Set("originator", "codex_cli_rs")
	header.Set("User-Agent", "codex_cli_rs/0.114.0 (Mac OS 15.6.0; arm64) Apple_Terminal/455")

	metadata := FromHeader(header)

	if metadata.SessionID != "session-1" || metadata.AgentID != "agent-2" {
		t.Fatalf("identity: %+v", metadata)
	}
	if metadata.ParentAgentID != "agent-1" || !metadata.IsSubagent {
		t.Fatalf("lineage: %+v", metadata)
	}
	if metadata.AgentName != "explorer" || metadata.AgentKind != "collab_spawn" {
		t.Fatalf("agent: %+v", metadata)
	}
	if metadata.TaskID != "" || metadata.TurnID != "turn-4" {
		t.Fatalf("turn metadata: %+v", metadata)
	}
	if got := metadata.RoutingIdentity(); got != "subagent:session-1:agent-2" {
		t.Fatalf("routing identity: %q", got)
	}
	if metadata.Originator != "codex_cli_rs" || metadata.ClientName != "codex_cli_rs" || metadata.ClientVersion != "0.114.0" {
		t.Fatalf("client: %+v", metadata)
	}
}

func TestClaudeCodeHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("x-claude-code-session-id", "session")
	header.Set("x-claude-code-agent-id", "agent")
	header.Set("x-claude-code-parent-agent-id", "parent")
	header.Set("x-client-request-id", "request")
	header.Set("User-Agent", "claude-cli/2.1.191 (external, cli)")

	metadata := FromHeader(header)

	if metadata.SessionID != "session" || metadata.AgentID != "agent" || metadata.ParentAgentID != "parent" || !metadata.IsSubagent {
		t.Fatalf("identity: %+v", metadata)
	}
	if metadata.CorrelationID != "request" || metadata.ClientName != "claude-cli" || metadata.ClientVersion != "2.1.191" {
		t.Fatalf("client: %+v", metadata)
	}
}

func TestCodexMetadataHeaderFallback(t *testing.T) {
	header := http.Header{}
	header.Set("x-codex-turn-metadata", `{"session_id":"session-1","thread_id":"agent-2","parent_thread_id":"agent-1","subagent_kind":"review"}`)

	metadata := FromHeader(header)

	if metadata.SessionID != "session-1" || metadata.AgentID != "agent-2" || metadata.ParentAgentID != "agent-1" {
		t.Fatalf("identity: %+v", metadata)
	}
	if !metadata.IsSubagent || metadata.AgentKind != "review" {
		t.Fatalf("subagent: %+v", metadata)
	}
}

func TestWingmanHeadersOverrideNativeHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("x-wingman-session-id", "override")
	header.Set("x-claude-code-session-id", "native")
	header.Set("x-claude-code-agent-id", "child")
	header.Set("x-wingman-is-subagent", "false")

	metadata := FromHeader(header)

	if metadata.SessionID != "override" {
		t.Fatalf("session: %q", metadata.SessionID)
	}
	if metadata.IsSubagent {
		t.Fatal("explicit root marker must override native child marker")
	}
	if got := metadata.RoutingIdentity(); got != "session:override" {
		t.Fatalf("routing identity: %q", got)
	}
}

func TestOpenCodeHeadersAndUserAgent(t *testing.T) {
	header := http.Header{}
	header.Set("x-session-affinity", "child")
	header.Set("X-Session-Id", "child")
	header.Set("x-parent-session-id", "root")
	header.Set("x-opencode-request", "request")
	header.Set("User-Agent", "opencode/1.15.7")

	metadata := FromHeader(header)

	if metadata.SessionID != "child" || metadata.ParentSessionID != "root" || !metadata.IsSubagent {
		t.Fatalf("opencode identity: %+v", metadata)
	}
	if metadata.CorrelationID != "request" {
		t.Fatalf("correlation: %+v", metadata)
	}
	if metadata.ClientName != "opencode" || metadata.ClientVersion != "1.15.7" {
		t.Fatalf("client: %+v", metadata)
	}
}

func TestOpenCodeHostedProviderHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("x-opencode-session", "session")
	header.Set("x-opencode-request", "request")
	header.Set("x-opencode-client", "tui")
	header.Set("User-Agent", "opencode/1.15.7")

	metadata := FromHeader(header)

	if metadata.SessionID != "session" || metadata.CorrelationID != "request" || metadata.Originator != "tui" {
		t.Fatalf("metadata: %+v", metadata)
	}
}

func TestCopilotRequestHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("X-Request-Id", "request")
	header.Set("X-Agent-Task-Id", "request")
	header.Set("X-Interaction-Type", "conversation-subagent")
	header.Set("X-Initiator", "agent")
	header.Set("Copilot-Integration-Id", "vscode-chat")
	header.Set("User-Agent", "GitHubCopilotChat/0.35.0")

	metadata := FromHeader(header)

	if metadata.CorrelationID != "request" || metadata.TaskID != "" {
		t.Fatalf("correlation: %+v", metadata)
	}
	if metadata.RequestKind != "conversation-subagent" || metadata.Initiator != "agent" || !metadata.IsSubagent {
		t.Fatalf("request kind: %+v", metadata)
	}
	if metadata.ClientName != "githubcopilotchat" || metadata.ClientVersion != "0.35.0" || metadata.Originator != "vscode-chat" {
		t.Fatalf("client: %+v", metadata)
	}
}

func TestOpenAIAgentsJavaScriptUserAgent(t *testing.T) {
	header := http.Header{"User-Agent": []string{"Agents/JavaScript 0.3.0"}}
	metadata := FromHeader(header)
	if metadata.ClientName != "agents/javascript" || metadata.ClientVersion != "0.3.0" {
		t.Fatalf("client: %+v", metadata)
	}
}

func TestContextRoundTrip(t *testing.T) {
	want := Metadata{SessionID: "session-1"}
	ctx := WithContext(context.Background(), want)

	got, ok := FromContext(ctx)
	if !ok || got != want {
		t.Fatalf("metadata: %+v, %v", got, ok)
	}
}
