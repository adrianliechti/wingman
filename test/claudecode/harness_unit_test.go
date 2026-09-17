package claudecode

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/harness"
)

const textStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"WINGMAN_E2E_OK"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

func TestRecorder(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.RequestURI() != "/gateway/v1/messages?beta=true" || req.Header.Get("X-Api-Key") != "upstream-secret" || req.Header.Get("Authorization") != "" {
			t.Errorf("proxy did not preserve path/query or replace authentication correctly")
		}
		body, _ := io.ReadAll(req.Body)
		if string(body) != `{"stream":true}` || req.Header.Get("Anthropic-Beta") != "test-beta" {
			t.Error("proxy changed request body or beta header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "secret-cookie")
		io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
		http.NewResponseController(w).Flush()
		select {
		case <-release:
		case <-req.Context().Done():
			return
		}
		io.WriteString(w, textStream)
	}))
	t.Cleanup(upstream.Close)
	r := newRecorder(t, harness.Endpoint{BaseURL: upstream.URL + "/gateway/v1/", APIKey: "upstream-secret"})
	req, _ := http.NewRequest("POST", r.URL+"/v1/messages?beta=true", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Authorization", "Bearer client-secret")
	req.Header.Set("X-Api-Key", "client-secret")
	req.Header.Set("Anthropic-Beta", "test-beta")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "event: ping\n" {
		t.Fatalf("first chunk did not stream before response completion: %q, %v", line, err)
	}
	unblock()
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if len(r.Exchanges()) != 1 || r.Exchanges()[0].Response != line+string(rest) || r.Exchanges()[0].Status != 200 {
		t.Fatal("recorder lost response bytes or status")
	}
	data, _ := json.Marshal(r.Exchanges())
	if strings.Contains(string(data), "secret") {
		t.Fatal("authentication or cookie leaked into trace")
	}
}

func TestValidateStream(t *testing.T) {
	toolStream := strings.Replace(textStream, `"type":"text","text":""`, `"type":"tool_use","id":"tool_1","name":"Read","input":{}`, 1)
	toolStream = strings.Replace(toolStream, `"type":"text_delta","text":"WINGMAN_E2E_OK"`, `"type":"input_json_delta","partial_json":"{\"file_path\":\"input.txt\"}"`, 1)
	toolStream = strings.Replace(toolStream, `"stop_reason":"end_turn"`, `"stop_reason":"tool_use"`, 1)
	tools, err := validateStream(toolStream)
	if err != nil || !reflect.DeepEqual(tools, map[string]string{"tool_1": "Read"}) {
		t.Fatalf("valid tool stream rejected: %v, %v", tools, err)
	}
	for _, tc := range []struct {
		name, stream string
		valid        bool
	}{
		{"text", textStream, true},
		{"missing_stop", strings.Split(textStream, "event: message_stop")[0], false},
		{"duplicate_start", textStream + textStream, false},
		{"missing_usage", strings.Replace(textStream, `"output_tokens":5`, `"other":5`, 1), false},
		{"unopened_block", strings.Replace(textStream, `"type":"content_block_delta","index":0`, `"type":"content_block_delta","index":1`, 1), false},
		{"unclosed_block", strings.Replace(textStream, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n", "", 1), false},
		{"invalid_tool_json", strings.Replace(toolStream, `input.txt\"}`, `input.txt\"`, 1), false},
		{"stream_error", strings.Replace(textStream, "event: message_stop\ndata: {\"type\":\"message_stop\"}", "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateStream(tc.stream)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}

// Exercise the installed CLI and subprocess flags against a local scripted
// API. This test never calls a paid provider; it skips if Claude is absent.
func TestClaudeCodeOffline(t *testing.T) {
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude Code is not installed")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/messages" {
			http.NotFound(w, req)
			return
		}
		var request struct {
			Tools []struct{ Name string } `json:"tools"`
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		names := map[string]bool{}
		for _, tool := range request.Tools {
			names[tool.Name] = true
		}
		if !reflect.DeepEqual(names, map[string]bool{"Read": true, "Edit": true}) {
			t.Errorf("fixture tools are unavailable: %v", names)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, textStream)
	}))
	t.Cleanup(upstream.Close)
	answer, exchanges, _ := runClaude(t, binary, harness.Endpoint{Name: "offline", BaseURL: upstream.URL + "/v1", APIKey: "test"}, "claude-sonnet-4-6", "Reply with WINGMAN_E2E_OK.", "Read,Edit", "")
	if answer != "WINGMAN_E2E_OK" {
		t.Errorf("unexpected CLI output: %q", answer)
	}
	checkExchanges(t, exchanges, "claude-sonnet-4-6")
}
