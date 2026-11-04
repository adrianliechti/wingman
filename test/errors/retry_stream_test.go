package errors

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
)

func TestFailedResponseRetryAdviceEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: " + `{"type":"response.output_text.delta","item_id":"msg_1","delta":"Hi"}` + "\n\n"))
		w.Write([]byte("data: " + `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"Try again in 1s","headers":{"rEtRy-AfTeR":"2.25"}}}}` + "\n\n"))
	}))
	defer upstream.Close()
	wingman := newWingmanServer(newResponder(upstream.URL, upstream.Client()), "test-model")
	defer wingman.Close()
	for _, stream := range []bool{false, true} {
		payload, _ := json.Marshal(map[string]any{"model": "test-model", "input": "x", "stream": stream})
		resp, err := wingman.Client().Post(wingman.URL+"/v1/responses", "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if !stream {
			if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "3" {
				t.Fatalf("HTTP status=%d retry=%q", resp.StatusCode, resp.Header.Get("Retry-After"))
			}
			continue
		}
		if resp.StatusCode != 200 {
			t.Fatalf("SSE HTTP status=%d", resp.StatusCode)
		}
		events, err := harness.ParseSSE(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		failed := false
		for _, e := range events {
			if e.Event != "response.failed" {
				continue
			}
			failed = true
			errorBody := e.Data["response"].(map[string]any)["error"].(map[string]any)
			if errorBody["code"] != "rate_limit_exceeded" || errorBody["headers"].(map[string]any)["Retry-After"] != "3" {
				t.Fatalf("retry advice lost: %+v", errorBody)
			}
		}
		if !failed {
			t.Fatal("missing response.failed")
		}
	}
}
