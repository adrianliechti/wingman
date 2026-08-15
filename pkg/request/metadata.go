// Package request normalizes transport metadata for request correlation,
// client identification and agent lineage.
//
// Metadata is carried in context so routing, telemetry, and providers can use it
// without adding transport-specific fields to provider requests.
package request

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	codexTurnMetadataHeader = "x-codex-turn-metadata"
	maxMetadataValueLength  = 512
)

type contextKey struct{}

// Metadata identifies a request within a conversation or agent run. Fields are
// optional because ordinary API clients do not necessarily send correlation
// headers.
type Metadata struct {
	SessionID string
	AgentID   string

	ParentAgentID   string
	ParentSessionID string
	IsSubagent      bool
	AgentName       string
	AgentKind       string

	TaskID string
	TurnID string

	SessionFinal   bool
	CorrelationID  string
	ConversationID string
	GenerationID   string
	RequestKind    string
	Initiator      string

	// UserAgent is the bounded original HTTP User-Agent value. ClientName and
	// ClientVersion are its normalized leading product token. These identify a
	// client implementation, never a user, session, or routing-affinity key.
	UserAgent     string
	ClientName    string
	ClientVersion string
	Originator    string
}

// WithContext attaches metadata to ctx. Empty metadata is still attached so a
// caller can distinguish normalized HTTP traffic from an internal call.
func WithContext(ctx context.Context, metadata Metadata) context.Context {
	return context.WithValue(ctx, contextKey{}, metadata)
}

// FromContext returns normalized request metadata, when present.
func FromContext(ctx context.Context) (Metadata, bool) {
	metadata, ok := ctx.Value(contextKey{}).(Metadata)
	return metadata, ok
}

// RoutingIdentity returns a stable cache/affinity key. Child agents are scoped
// below their session so independent workers do not inherit the root decision.
func (m Metadata) RoutingIdentity() string {
	if m.IsSubagent && m.SessionID != "" && m.AgentID != "" {
		return "subagent:" + m.SessionID + ":" + m.AgentID
	}

	if m.SessionID != "" {
		return "session:" + m.SessionID
	}

	return ""
}

// FromHeader normalizes Wingman override headers and common coding-agent
// correlation headers. Explicit x-wingman-* values take precedence.
func FromHeader(header http.Header) Metadata {
	codex := codexMetadata(header)
	userAgent := truncate(strings.TrimSpace(header.Get("User-Agent")), maxMetadataValueLength)
	clientName, clientVersion := clientProduct(userAgent)

	metadata := Metadata{
		SessionID: firstHeader(header,
			"x-wingman-session-id",
			"x-claude-code-session-id",
			"x-opencode-session",
			"session-id",
			"x-session-affinity",
			"x-session-id",
		),
		AgentID: firstHeader(header,
			"x-wingman-agent-id",
			"x-claude-code-agent-id",
			"thread-id",
		),
		ParentAgentID: firstHeader(header,
			"x-wingman-parent-agent-id",
			"x-claude-code-parent-agent-id",
			"x-codex-parent-thread-id",
		),
		ParentSessionID: firstHeader(header,
			"x-wingman-parent-session-id",
			"x-parent-session-id",
		),
		AgentName: firstHeader(header,
			"x-wingman-agent-name",
		),
		AgentKind: firstHeader(header,
			"x-wingman-agent-kind",
			"x-openai-subagent",
		),
		TaskID: firstHeader(header,
			"x-wingman-task-id",
		),
		TurnID: firstHeader(header,
			"x-wingman-turn-id",
		),
		CorrelationID: firstHeader(header,
			"x-wingman-request-id",
			"x-client-request-id",
			"x-opencode-request",
			"x-request-id",
		),
		ConversationID: firstHeader(header,
			"x-wingman-conversation-id",
		),
		GenerationID: firstHeader(header,
			"x-wingman-generation-id",
		),
		RequestKind: firstHeader(header,
			"x-wingman-request-kind",
			"x-interaction-type",
		),
		Initiator: firstHeader(header,
			"x-wingman-initiator",
			"x-initiator",
		),
		UserAgent:     userAgent,
		ClientName:    clientName,
		ClientVersion: clientVersion,
		Originator: truncate(firstHeader(header,
			"x-wingman-originator",
			"originator",
			"x-opencode-client",
			"copilot-integration-id",
		), 128),
	}

	if metadata.SessionID == "" {
		metadata.SessionID = stringField(codex, "session_id")
	}
	if metadata.AgentID == "" {
		metadata.AgentID = stringField(codex, "thread_id")
	}
	if metadata.ParentAgentID == "" {
		metadata.ParentAgentID = stringField(codex, "parent_thread_id")
	}
	if metadata.AgentName == "" {
		metadata.AgentName = stringField(codex, "agent_name")
	}
	if metadata.AgentKind == "" {
		metadata.AgentKind = stringField(codex, "subagent_kind")
	}
	if metadata.TurnID == "" {
		metadata.TurnID = stringField(codex, "turn_id")
	}

	explicitSubagent, hasExplicitSubagent := boolHeader(header, "x-wingman-is-subagent")
	_, claudeSubagent := nonemptyHeader(header, "x-claude-code-agent-id")
	_, openCodeParent := nonemptyHeader(header, "x-parent-session-id")
	_, codexParent := nonemptyHeader(header, "x-codex-parent-thread-id")
	codexSubagent := metadata.AgentKind != "" || codexParent
	copilotSubagent := strings.EqualFold(metadata.RequestKind, "conversation-subagent")

	if hasExplicitSubagent {
		metadata.IsSubagent = explicitSubagent
	} else {
		metadata.IsSubagent = claudeSubagent || openCodeParent || codexSubagent || copilotSubagent
	}

	metadata.SessionFinal, _ = boolHeader(header, "x-wingman-session-final")

	return metadata
}

func codexMetadata(header http.Header) map[string]any {
	value, ok := nonemptyHeader(header, codexTurnMetadataHeader)
	if !ok {
		return nil
	}

	var result map[string]any
	if json.Unmarshal([]byte(value), &result) != nil {
		return nil
	}

	return result
}

func clientProduct(userAgent string) (string, string) {
	fields := strings.Fields(userAgent)
	if len(fields) == 0 {
		return "", ""
	}
	if strings.EqualFold(fields[0], "Agents/JavaScript") {
		version := ""
		if len(fields) > 1 {
			version = fields[1]
		}
		return "agents/javascript", version
	}

	name, version, found := strings.Cut(fields[0], "/")
	if !found {
		return strings.ToLower(name), ""
	}

	return strings.ToLower(name), version
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func stringField(values map[string]any, name string) string {
	value, ok := values[name].(string)
	if !ok {
		return ""
	}

	return truncate(strings.TrimSpace(value), maxMetadataValueLength)
}

func firstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value, ok := nonemptyHeader(header, name); ok {
			return truncate(value, maxMetadataValueLength)
		}
	}

	return ""
}

func nonemptyHeader(header http.Header, name string) (string, bool) {
	value := strings.TrimSpace(header.Get(name))
	return value, value != ""
}

func boolHeader(header http.Header, name string) (bool, bool) {
	value, ok := nonemptyHeader(header, name)
	if !ok {
		return false, false
	}

	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}
