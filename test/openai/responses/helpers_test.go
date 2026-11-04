package responses

import (
	"encoding/json/v2"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
)

func TestWithModelPreservesExplicitReasoning(t *testing.T) {
	for _, effort := range []string{"", "high"} {
		body := map[string]any{"input": "hello"}
		if effort != "" {
			body["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
		}
		original := body["reasoning"]
		got := WithModel(body, "gpt-6-luna")
		want := original
		if effort == "" {
			want = map[string]any{"effort": "none"}
		}
		if !reflect.DeepEqual(got["reasoning"], want) {
			t.Fatalf("reasoning = %v, want %v", got["reasoning"], want)
		}
		if !reflect.DeepEqual(body["reasoning"], original) || body["model"] != nil {
			t.Fatal("changed the shared fixture")
		}
	}
}

func TestOptionalReasoningComparisonPreservesOtherOutput(t *testing.T) {
	var events []*harness.SSEEvent
	if err := json.Unmarshal([]byte(`[
		{"event":"response.output_item.added","data":{"output_index":0,"item":{"type":"reasoning"}}},
		{"event":"response.output_item.added","data":{"output_index":1,"item":{"type":"message","id":"msg_1"}}},
		{"event":"response.output_text.delta","data":{"output_index":1,"delta":"hello"}},
		{"event":"response.output_item.added","data":{"output_index":2,"item":{"type":"function_call","name":"record"}}},
		{"event":"response.completed","data":{"response":{"output":[{"type":"reasoning"},{"type":"message","id":"msg_1"},{"type":"function_call","name":"record"}]}}}
	]`), &events); err != nil {
		t.Fatal(err)
	}
	got := withoutReasoningEvents(events)
	if len(got) != 4 || got[0].Data["output_index"] != float64(0) || got[1].Data["output_index"] != float64(0) || got[2].Data["output_index"] != float64(1) {
		t.Fatalf("incorrect output indices after optional reasoning: %+v", got)
	}
	if got[1].Data["delta"] != "hello" || got[2].Data["item"].(map[string]any)["name"] != "record" {
		t.Fatal("lost text or function output")
	}
	output := got[3].Data["response"].(map[string]any)["output"].([]any)
	if len(output) != 2 || output[0].(map[string]any)["id"] != "msg_1" || output[1].(map[string]any)["name"] != "record" {
		t.Fatalf("incorrect terminal output: %v", output)
	}
	if events[1].Data["output_index"] != float64(1) || len(events[4].Data["response"].(map[string]any)["output"].([]any)) != 3 {
		t.Fatal("comparison changed the original stream")
	}
}
