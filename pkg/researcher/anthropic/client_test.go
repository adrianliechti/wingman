package anthropic

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/researcher"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func streamResponse(events ...string) *http.Response {
	var body strings.Builder
	for _, event := range events {
		var metadata struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &metadata)
		fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", metadata.Type, event)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body.String()))}
}

const messageStart = `{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"test","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`

func TestResearchStreamProgressAndCitations(t *testing.T) {
	client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if version := req.Header.Get("anthropic-version"); version != "2026-10-21" {
			t.Errorf("anthropic-version = %q", version)
		}
		if req.Header.Get("Authorization") != "Bearer test" || req.Header.Get("X-Api-Key") != "" {
			t.Error("expected bearer authentication without X-Api-Key")
		}
		var body map[string]any
		if err := json.UnmarshalRead(req.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true {
			t.Fatal("streaming not requested")
		}
		return streamResponse(messageStart,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"Searching now."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"search-1","name":"web_search","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"MCP progress\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"web_search_tool_result","tool_use_id":"search-1","content":[{"type":"web_search_result","url":"https://example.com","title":"MCP","encrypted_content":"test"}]}}`,
			`{"type":"content_block_stop","index":2}`,
			`{"type":"content_block_start","index":3,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"Cited report."}}`,
			`{"type":"content_block_delta","index":3,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://example.com","title":"MCP","cited_text":"report","encrypted_index":"test"}}}`,
			`{"type":"content_block_stop","index":3}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`,
			`{"type":"message_stop"}`), nil
	})}))
	var progress []string
	var report string
	for result, err := range client.Research(t.Context(), "q", nil) {
		if err != nil {
			t.Fatal(err)
		}
		if result.Progress != "" {
			progress = append(progress, result.Progress)
		} else {
			report = result.Content
		}
	}
	for _, want := range []string{"Searching the web: MCP progress", "Web search returned 1 sources", "Writing research report"} {
		if !strings.Contains(strings.Join(progress, "|"), want) {
			t.Fatalf("missing %q in %v", want, progress)
		}
	}
	if report != "Cited report.\n\nSources:\n- MCP: https://example.com" {
		t.Fatalf("report = %q", report)
	}
}

func TestResearchContinuesPausedTurn(t *testing.T) {
	calls := 0
	client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.UnmarshalRead(req.Body, &body); err != nil {
			t.Fatal(err)
		}
		if calls == 1 {
			return streamResponse(messageStart,
				`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"search-1","name":"web_search","input":{"query":"MCP"}}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"pause_turn","container":{"id":"container-1"}},"usage":{"output_tokens":1}}`,
				`{"type":"message_stop"}`), nil
		}
		messages := body["messages"].([]any)
		if len(messages) != 2 || body["container"] != "container-1" {
			t.Fatalf("continuation = %+v", body)
		}
		assistant := messages[1].(map[string]any)
		if assistant["role"] != "assistant" || !strings.Contains(fmt.Sprint(assistant["content"]), "MCP") {
			t.Fatalf("lost paused content: %+v", assistant)
		}
		return streamResponse(messageStart,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"Final report."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`), nil
	})}))
	result, err := researcher.Collect(client.Research(t.Context(), "q", nil))
	if err != nil || calls != 2 || result.Content != "Final report." {
		t.Fatalf("result = %+v, calls = %d, err = %v", result, calls, err)
	}
}

func TestResearchBoundsPausedTurns(t *testing.T) {
	calls := 0
	client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return streamResponse(messageStart,
			`{"type":"message_delta","delta":{"stop_reason":"pause_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`), nil
	})}))
	if result, err := researcher.Collect(client.Research(t.Context(), "q", nil)); err == nil || calls != maxContinuations+1 {
		t.Fatalf("result = %+v, calls = %d, err = %v", result, calls, err)
	}
}

func TestResearchRejectsTruncatedStream(t *testing.T) {
	for _, maxTokens := range []bool{false, true} {
		client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			events := []string{messageStart, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"partial"}}`}
			if maxTokens {
				events = append(events, `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":1}}`, `{"type":"message_stop"}`)
			}
			return streamResponse(events...), nil
		})}))
		if result, err := researcher.Collect(client.Research(t.Context(), "q", nil)); err == nil || result != nil {
			t.Fatalf("result = %+v, err = %v", result, err)
		}
	}
}

func TestFetchedDocumentCitationsRetainSourceURLs(t *testing.T) {
	client, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return streamResponse(messageStart,
			`{"type":"content_block_start","index":0,"content_block":{"type":"web_fetch_tool_result","tool_use_id":"fetch-1","content":{"type":"web_fetch_result","url":"https://example.com/report","content":{"type":"document","title":"Source report","source":{"type":"text","media_type":"text/plain","data":"Evidence"},"citations":{"enabled":true}}}}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"Cited answer.","citations":[{"type":"char_location","document_index":0,"document_title":"Source report","start_char_index":0,"end_char_index":8,"cited_text":"Evidence"}]}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`), nil
	})}))
	result, err := researcher.Collect(client.Research(t.Context(), "q", nil))
	if err != nil || result.Content != "Cited answer.\n\nSources:\n- Source report: https://example.com/report" {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestAppendSources(t *testing.T) {
	if got := appendSources("answer", nil); got != "answer" {
		t.Errorf("no sources: got %q, want %q", got, "answer")
	}

	sources := []source{
		{title: "Example", url: "https://example.com"},
		{title: "", url: "https://example.org"},
	}

	got := appendSources("answer", sources)

	if !strings.HasPrefix(got, "answer\n\nSources:") {
		t.Errorf("expected content followed by sources, got %q", got)
	}

	if !strings.Contains(got, "Example: https://example.com") {
		t.Errorf("expected titled source line, got %q", got)
	}

	if !strings.Contains(got, "- https://example.org") {
		t.Errorf("expected untitled source line, got %q", got)
	}
}
