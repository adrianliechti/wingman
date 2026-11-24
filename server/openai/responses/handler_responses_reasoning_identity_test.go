package responses

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

func TestResponsesStreamKeepsReasoningItemIdentity(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		doneCiphertext                 string
		finalCiphertext                string
		doneText, finalText            string
		trailing, incomplete, omitDone bool
		rawContent                     bool
		plain, legacyInclude           bool
	}{
		{name: "plain stateless request", plain: true, doneText: "hidden", finalText: "hidden revision", doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-b"},
		{name: "legacy include only", legacyInclude: true, doneText: "hidden", finalText: "hidden revision", doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-b"},
		{name: "plain stateless terminal-only ciphertext", plain: true, rawContent: true, omitDone: true, finalText: "raw hidden", finalCiphertext: "encrypted-b"},
		{name: "unchanged ciphertext", doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-a"},
		{name: "changed ciphertext", doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-b"},
		{name: "ciphertext in final snapshot only", finalCiphertext: "encrypted-b"},
		{name: "incomplete with terminal-only ciphertext", incomplete: true, finalCiphertext: "encrypted-b"},
		{name: "ciphertext in done only", doneCiphertext: "encrypted-a"},
		{name: "terminal summary suffix", doneText: "First", finalText: "First and second", doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-b"},
		{name: "terminal summary replacement", doneText: "Draft", finalText: "Final", doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-b"},
		{name: "reasoning text revised at completion", rawContent: true, doneText: "Draft", finalText: "Revision"},
		{name: "empty final summary", doneText: "Draft"},
		{name: "upstream omitted item done", omitDone: true, finalText: "Recovered", finalCiphertext: "encrypted-b"},
		{name: "trailing reasoning", trailing: true, doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-b"},
		{name: "incomplete trailing reasoning", trailing: true, incomplete: true, doneCiphertext: "encrypted-a", finalCiphertext: "encrypted-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The terminal snapshot has the same reasoning item and output
			// index as the item events, but can carry a different ciphertext.
			status := "completed"
			if tc.incomplete {
				status = "incomplete"
			}
			type part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			type item struct {
				ID               string `json:"id"`
				Type             string `json:"type"`
				EncryptedContent string `json:"encrypted_content,omitempty"`
				Summary          []part `json:"summary,omitempty"`
				Content          []part `json:"content,omitempty"`
			}
			reasoning := func(summary, signature, status string) map[string]any {
				parts := []part{}
				if summary != "" {
					parts = append(parts, part{"summary_text", summary})
				}
				value := map[string]any{"id": "rs_fixture", "type": "reasoning", "status": status, "summary": parts, "encrypted_content": signature}
				if tc.rawContent {
					value["summary"] = []part{}
					for i := range parts {
						parts[i].Type = "reasoning_text"
					}
					value["content"] = parts
				}
				return value
			}
			var upstream strings.Builder
			sequence := 0
			emit := func(kind string, fields map[string]any) {
				fields["type"], fields["sequence_number"] = kind, sequence
				sequence++
				data, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&upstream, "event: %s\ndata: %s\n\n", kind, data)
			}
			response := func(status string, output []any) map[string]any {
				return map[string]any{"response": map[string]any{"id": "resp_fixture", "model": "gpt-6-sol", "status": status, "output": output}}
			}
			emit("response.created", response("in_progress", []any{}))
			emit("response.output_item.added", map[string]any{"output_index": 0, "item": reasoning("", "partial-not-for-replay", "in_progress")})
			if !tc.omitDone {
				emit("response.output_item.done", map[string]any{"output_index": 0, "item": reasoning(tc.doneText, tc.doneCiphertext, status)})
			}
			output := []any{reasoning(tc.finalText, tc.finalCiphertext, status)}
			if !tc.trailing {
				message := map[string]any{"type": "message", "id": "msg_fixture", "role": "assistant", "status": "in_progress", "content": []any{}}
				emit("response.output_item.added", map[string]any{"output_index": 1, "item": message})
				message["status"], message["content"] = "completed", []part{{"output_text", "Answer"}}
				emit("response.output_item.done", map[string]any{"output_index": 1, "item": message})
				output = append(output, message)
			}
			emit("response."+status, response(status, output))
			client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
				var req struct {
					Include []string `json:"include"`
				}
				if err := json.UnmarshalRead(r.Body, &req); err != nil {
					t.Fatal(err)
				}
				if slices.Contains(req.Include, "reasoning.encrypted_content") != tc.legacyInclude {
					t.Errorf("upstream include = %v, explicit legacy include = %t", req.Include, tc.legacyInclude)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(upstream.String())),
					Request:    r,
				}, nil
			})}
			completer, err := openai.NewResponder("http://upstream.test", "gpt-6-sol", openai.WithToken("test"), openai.WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			request := `{"model":"test","input":"check","stream":true,"store":false,"reasoning":{"effort":"high"}}`
			if tc.plain {
				request = `{"model":"test","input":"check","stream":true,"store":false}`
			}
			if tc.legacyInclude {
				request = `{"model":"test","input":"check","stream":true,"store":false,"include":["reasoning.encrypted_content"]}`
			}
			payload, wire := reviewRequestBody(t, signatures.ScopedTo("gpt-6-sol", completer), true, request)

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
				if tc.plain && (strings.HasPrefix(event.Type, "response.reasoning_") || strings.Contains(data, "hidden")) {
					t.Errorf("plain request exposed reasoning text: %s", data)
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
			wantItem := func(summary, signature string) item {
				value := item{ID: "rs_fixture", Type: "reasoning", Summary: []part{}, Content: []part{}}
				if summary != "" && !tc.plain {
					if tc.rawContent {
						value.Content = []part{{"reasoning_text", summary}}
					} else {
						value.Summary = append(value.Summary, part{"summary_text", summary})
					}
				}
				if signature != "" {
					value.EncryptedContent = "@gpt-6-sol:" + signature
				}
				return value
			}
			wantAdded := []item{wantItem("", "")}
			wantDone := []item{wantItem(tc.doneText, cmp.Or(tc.doneCiphertext, tc.finalCiphertext))}
			wantFinal := []item{wantItem(tc.finalText, cmp.Or(tc.finalCiphertext, tc.doneCiphertext))}
			if !tc.trailing {
				message := item{ID: "msg_fixture", Type: "message", Content: []part{}}
				wantAdded = append(wantAdded, message)
				message.Content = []part{{"output_text", "Answer"}}
				wantDone = append(wantDone, message)
				wantFinal = append(wantFinal, message)
			}
			if !reflect.DeepEqual(added, wantAdded) || !reflect.DeepEqual(done, wantDone) {
				t.Errorf("added=%v, done=%v, want added=%v, done=%v", added, done, wantAdded, wantDone)
			}
			if result.Status != status || !reflect.DeepEqual(result.Output, wantFinal) {
				t.Errorf("status=%q, output=%v, want status=%s, output=%v", result.Status, result.Output, status, wantFinal)
			}
			payload, _ = reviewRequestBody(t, signatures.ScopedTo("gpt-6-sol", completer), false, strings.Replace(request, `"stream":true`, `"stream":false`, 1))
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != status || !reflect.DeepEqual(result.Output, wantFinal) {
				t.Errorf("HTTP status=%q, output=%v, want status=%s, output=%v", result.Status, result.Output, status, wantFinal)
			}
		})
	}
}
