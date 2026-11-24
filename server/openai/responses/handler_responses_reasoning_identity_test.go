package responses

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func TestResponsesStreamKeepsReasoningItemIdentity(t *testing.T) {
	for _, tc := range []struct {
		name            string
		doneCiphertext  string
		finalCiphertext string
	}{
		{name: "unchanged ciphertext", doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-a"},
		{name: "changed ciphertext", doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-b"},
		{name: "ciphertext in final snapshot only", finalCiphertext: "encrypted-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The terminal snapshot has the same reasoning item and output
			// index as the item events, but can carry a different ciphertext.
			const reasoning = `{"type":"reasoning","id":"rs_fixture","status":"completed","summary":[],"encrypted_content":%q}`
			const message = `{"type":"message","id":"msg_fixture","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Answer","annotations":[]}]}`
			upstream := fmt.Sprintf(`event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_fixture","model":"gpt-6-sol","status":"in_progress","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"reasoning","id":"rs_fixture","status":"in_progress","summary":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":%s}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":3,"output_index":1,"item":{"type":"message","id":"msg_fixture","role":"assistant","status":"in_progress","content":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":4,"output_index":1,"item":%s}

event: response.completed
data: {"type":"response.completed","sequence_number":5,"response":{"id":"resp_fixture","model":"gpt-6-sol","status":"completed","output":[%s,%s]}}

`, fmt.Sprintf(reasoning, tc.doneCiphertext), message, fmt.Sprintf(reasoning, tc.finalCiphertext), message)
			client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(upstream)),
					Request:    r,
				}, nil
			})}
			completer, err := openai.NewResponder("http://upstream.test", "gpt-6-sol", openai.WithToken("test"), openai.WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			const request = `{"model":"test","input":"check","stream":true,"store":false,"reasoning":{"effort":"high"},"include":["reasoning.encrypted_content"]}`
			payload, wire := reviewRequestBody(t, signatures.ScopedTo("gpt-6-sol", completer), true, request)

			type item struct {
				ID               string `json:"id"`
				Type             string `json:"type"`
				EncryptedContent string `json:"encrypted_content"`
			}
			var added, done []item
			addedIndexes := map[string]int{}
			for line := range strings.SplitSeq(wire, "\n") {
				data, ok := strings.CutPrefix(line, "data: ")
				if !ok {
					continue
				}
				var event struct {
					Type        string `json:"type"`
					OutputIndex int    `json:"output_index"`
					Item        item   `json:"item"`
				}
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					t.Fatal(err)
				}
				switch event.Type {
				case "response.output_item.added":
					if index, exists := addedIndexes[event.Item.ID]; exists {
						t.Errorf("item id %q reused at output_index=%d and output_index=%d", event.Item.ID, index, event.OutputIndex)
					}
					if event.OutputIndex != len(added) {
						t.Errorf("item %q added at output_index=%d, want %d", event.Item.ID, event.OutputIndex, len(added))
					}
					addedIndexes[event.Item.ID] = event.OutputIndex
					added = append(added, event.Item)
				case "response.output_item.done":
					if index, exists := addedIndexes[event.Item.ID]; !exists || index != event.OutputIndex {
						t.Errorf("item %q completed at output_index=%d without matching add", event.Item.ID, event.OutputIndex)
					}
					done = append(done, event.Item)
				}
			}
			var result struct {
				Status string `json:"status"`
				Output []item `json:"output"`
			}
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			wantAdded := []item{{ID: "rs_fixture", Type: "reasoning"}, {ID: "msg_fixture", Type: "message"}}
			wantDone, wantFinal := slices.Clone(wantAdded), slices.Clone(wantAdded)
			if tc.doneCiphertext != "" {
				wantDone[0].EncryptedContent = "@gpt-6-sol:" + tc.doneCiphertext
			}
			wantFinal[0].EncryptedContent = "@gpt-6-sol:" + tc.finalCiphertext
			if !slices.Equal(added, wantAdded) || !slices.Equal(done, wantDone) {
				t.Errorf("added=%v, done=%v, want added=%v, done=%v", added, done, wantAdded, wantDone)
			}
			if result.Status != "completed" || !slices.Equal(result.Output, wantFinal) {
				t.Errorf("status=%q, completed output=%v, want status=completed, output=%v", result.Status, result.Output, wantFinal)
			}
		})
	}
}
