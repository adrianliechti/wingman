package google

import (
	"encoding/json/v2"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"
)

func TestConvertTools_CustomEmulated(t *testing.T) {
	tools, err := convertTools([]provider.Tool{{Kind: provider.ToolKindCustom, Name: "run_python", Description: "Run a script."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Function == nil || value(tools[0].Function.Name) != "run_python" {
		t.Fatalf("tool lost: %+v", tools)
	}
	schema, _ := tools[0].Function.Parameters.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	input, _ := props[custom.InputParameter].(map[string]any)
	if len(props) != 1 || input["type"] != "string" {
		t.Fatalf("custom input schema lost: %+v", schema)
	}
}

func TestComplete_CustomToolUnwrapsStreamedInput(t *testing.T) {
	source := "print(\"alpha — beta\")\n"
	arguments := custom.Wrap(source)
	events := []string{createdEvent, `{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"c1","name":"scripts_run_python","arguments":{}}}`}
	for _, fragment := range []string{arguments[:8], arguments[8:]} {
		data, _ := json.Marshal(map[string]any{"event_type": "step.delta", "index": 0, "delta": map[string]any{"type": "arguments_delta", "arguments": fragment}})
		events = append(events, string(data))
	}
	events = append(events, `{"event_type":"step.stop","index":0}`, `{"event_type":"interaction.completed","interaction":{"id":"r1","status":"requires_action"}}`)
	options := &provider.CompleteOptions{Tools: []provider.Tool{{Name: "scripts", Tools: []provider.Tool{{Name: "run_python", Kind: provider.ToolKindCustom}}}}}
	deltas, err := completeAll(t, interactionEvents(events...), options)
	if err != nil {
		t.Fatal(err)
	}
	result := accumulated(deltas)
	calls := result.Message.ToolCalls()
	if len(calls) != 1 || calls[0].Kind != provider.ToolKindCustom || calls[0].Name != "run_python" || calls[0].Namespace != "scripts" || calls[0].Arguments != source {
		t.Fatalf("custom input lost: %+v", calls)
	}
	steps, err := convertMessages([]provider.Message{*result.Message})
	if err != nil || len(steps) != 2 || value(steps[0].ThoughtStep.Signature) != unsignedThoughtSignature || steps[1].FunctionCallStep.Name != "scripts_run_python" || steps[1].FunctionCallStep.Arguments[custom.InputParameter] != source {
		t.Fatalf("custom tool does not replay: %+v / %v", steps, err)
	}
}
