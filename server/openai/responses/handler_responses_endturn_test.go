package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type endTurnCompleter []provider.Completion

func (chunks endTurnCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for _, chunk := range chunks {
			if !yield(&chunk, nil) {
				return
			}
		}
	}
}

func assertResponseEndTurn(t *testing.T, completer provider.Completer, stream bool, want *bool) {
	t.Helper()
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("end-turn-test", completer)
	body := fmt.Sprintf(`{"model":"end-turn-test","input":"check it","stream":%t}`, stream)
	rec := httptest.NewRecorder()
	New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	check := func(response map[string]any) {
		t.Helper()
		if want != nil && response["status"] != "completed" {
			t.Fatalf("status=%v; completed commentary and tool responses must keep the public API's completed status", response["status"])
		}
		got, exists := response["end_turn"]
		if want == nil {
			if exists && got != nil {
				t.Fatalf("end_turn=%v; expected no turn-completion claim", got)
			}
		} else if !exists || got != *want {
			t.Fatalf("end_turn=%v (present=%t), want %t", got, exists, *want)
		}
	}
	if !stream {
		var response map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		check(response)
		return
	}
	terminal := 0
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type     string         `json:"type"`
			Response map[string]any `json:"response"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case "response.completed", "response.incomplete":
			terminal++
			check(event.Response)
		case "response.created", "response.in_progress":
			if value := event.Response["end_turn"]; value != nil {
				t.Fatalf("%s prematurely sets end_turn=%v", event.Type, value)
			}
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal responses=%d, want 1: %s", terminal, rec.Body.String())
	}
}

func TestResponsesEndTurn(t *testing.T) {
	commentary := provider.Message{Role: provider.MessageRoleAssistant, Phase: provider.MessagePhaseCommentary, Content: []provider.Content{provider.TextContent("Checking.")}}
	final := provider.Message{Role: provider.MessageRoleAssistant, Phase: provider.MessagePhaseFinalAnswer, Content: []provider.Content{provider.TextContent("Done.")}}
	unphased := provider.AssistantMessage("Done.")
	refusal := provider.Message{Role: provider.MessageRoleAssistant, Phase: provider.MessagePhaseCommentary, Content: []provider.Content{provider.RefusalContent("Cannot answer.")}}
	call := provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "check", Arguments: `{}`})}}
	hosted := provider.Message{Role: provider.MessageRoleAssistant, Phase: provider.MessagePhaseFinalAnswer, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "check", Execution: "server", Arguments: `{}`}), provider.TextContent("Done.")}}
	for _, tc := range []struct {
		name   string
		chunks endTurnCompleter
		want   *bool
	}{
		{"commentary", endTurnCompleter{{Message: &commentary}}, new(false)},
		{"commentary then final", endTurnCompleter{{Message: &commentary}, {Message: &final}}, new(true)},
		{"unphased answer", endTurnCompleter{{Message: &unphased}}, new(true)},
		{"tool call", endTurnCompleter{{Message: &call}}, new(false)},
		{"hosted tool then final", endTurnCompleter{{Message: &hosted}}, new(true)},
		{"refusal", endTurnCompleter{{Message: &refusal}, {Status: provider.CompletionStatusRefused}}, new(true)},
		{"pause after final-looking text", endTurnCompleter{{Message: &final}, {StopReason: provider.StopReasonPauseTurn}}, new(false)},
		{"pause without output", endTurnCompleter{{StopReason: provider.StopReasonPauseTurn}}, new(false)},
		{"tool-use boundary", endTurnCompleter{{Message: &unphased}, {StopReason: provider.StopReasonToolUse}}, new(false)},
		{"compaction boundary", endTurnCompleter{{StopReason: provider.StopReasonCompaction}}, new(false)},
		{"explicit end after commentary", endTurnCompleter{{Message: &commentary}, {StopReason: provider.StopReasonEndTurn}}, new(true)},
		{"stop sequence", endTurnCompleter{{Message: &commentary}, {StopReason: provider.StopReasonStopSequence}}, new(true)},
		{"truncated commentary", endTurnCompleter{{Message: &commentary}, {Status: provider.CompletionStatusIncomplete}}, nil},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				assertResponseEndTurn(t, tc.chunks, stream, tc.want)
			})
		}
	}
}
