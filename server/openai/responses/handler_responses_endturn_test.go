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
			t.Fatalf("status=%v; a native boundary must not change the documented status", response["status"])
		}
		got, exists := response["end_turn"]
		if want == nil {
			if exists {
				t.Fatalf("end_turn=%v; expected no turn-boundary claim", got)
			}
		} else if !exists || got != *want {
			t.Fatalf("end_turn=%v (present=%t), want %t", got, exists, *want)
		}
		// The boundary travels in Codex's field only; no other undocumented field appears.
		if _, alien := response["stop_reason"]; alien {
			t.Fatal("response carries an undocumented stop_reason")
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
	for line := range strings.SplitSeq(rec.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var event struct {
			Type     string         `json:"type"`
			Response map[string]any `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case "response.completed", "response.incomplete":
			terminal++
			check(event.Response)
		case "response.created", "response.in_progress":
			if _, exists := event.Response["end_turn"]; exists {
				t.Fatalf("%s sets end_turn before the response ended", event.Type)
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
	refusal := provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.RefusalContent("Cannot answer.")}}
	call := provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "check", Arguments: `{}`})}}
	for _, tc := range []struct {
		name   string
		chunks endTurnCompleter
		want   *bool
	}{
		// Providers without a native boundary: the gateway makes no claim.
		{"commentary without boundary", endTurnCompleter{{Message: &commentary}}, nil},
		{"final answer without boundary", endTurnCompleter{{Message: &final}}, nil},
		{"unphased answer without boundary", endTurnCompleter{{Message: &unphased}}, nil},
		{"tool call without boundary", endTurnCompleter{{Message: &call}}, nil},
		// The caller's tool results continue a tool-use turn, as they do upstream.
		{"tool-use boundary", endTurnCompleter{{Message: &call}, {StopReason: provider.StopReasonToolUse}}, new(false)},
		{"native end of turn", endTurnCompleter{{Message: &unphased}, {StopReason: provider.StopReasonEndTurn}}, new(true)},
		{"native end after commentary", endTurnCompleter{{Message: &commentary}, {StopReason: provider.StopReasonEndTurn}}, new(true)},
		{"stop sequence", endTurnCompleter{{Message: &unphased}, {StopReason: provider.StopReasonStopSequence}}, new(true)},
		{"refusal", endTurnCompleter{{Message: &refusal}, {Status: provider.CompletionStatusRefused}}, new(true)},
		{"paused after text", endTurnCompleter{{Message: &unphased}, {StopReason: provider.StopReasonPauseTurn}}, new(false)},
		{"paused without output", endTurnCompleter{{StopReason: provider.StopReasonPauseTurn}}, new(false)},
		{"compaction boundary", endTurnCompleter{{StopReason: provider.StopReasonCompaction}}, new(false)},
		{"truncated", endTurnCompleter{{Message: &unphased}, {Status: provider.CompletionStatusIncomplete, StopReason: provider.StopReasonMaxTokens}}, nil},
		// A backend that reports the boundary itself is passed through, including a request to continue.
		{"upstream continues after final answer", endTurnCompleter{{Message: &final}, {EndTurn: new(false)}}, new(false)},
		{"upstream ends after commentary", endTurnCompleter{{Message: &commentary}, {EndTurn: new(true)}}, new(true)},
		{"upstream claim over tool-use boundary", endTurnCompleter{{Message: &call}, {StopReason: provider.StopReasonToolUse, EndTurn: new(true)}}, new(true)},
		{"upstream claim on a truncated response", endTurnCompleter{{Message: &unphased}, {Status: provider.CompletionStatusIncomplete, EndTurn: new(true)}}, nil},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				assertResponseEndTurn(t, tc.chunks, stream, tc.want)
			})
		}
	}
}
