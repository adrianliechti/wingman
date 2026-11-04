package responses

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/jsonutil"
)

func TestInputUnionsV2(t *testing.T) {
	// Discriminators are last, and opaque values must survive every nested codec.
	const input = `[
 {"content":[{"text":"hello","type":"input_text"}],"role":"user","type":"message"},
 {"output":"","call_id":"f","type":"function_call_output"},
 {"output":[{"text":"done","type":"output_text"}],"call_id":"c","type":"custom_tool_call_output"},
 {"tools":[{"type":"function","name":"lookup","parameters":{"enum":[9007199254740993,1e400]}}],"type":"additional_tools"},
 {"output":{"id":9007199254740993},"call_id":"computer","type":"computer_call_output"}
 ]`
	var value ResponsesInput
	if err := jsonutil.Unmarshal([]byte(input), &value); err != nil {
		t.Fatal(err)
	}
	if len(value.Items) != 5 || value.Items[0].InputMessage.Content[0].Text != "hello" ||
		value.Items[1].InputFunctionCallOutput.Output[0].Text != "" ||
		value.Items[2].InputCustomToolCallOutput.Output[0].Text != "done" {
		t.Fatalf("wrong union values: %+v", value)
	}
	for _, item := range []any{value.Items[3].InputAdditionalTools.Tools[0].Parameters, value.Items[4].InputComputerCallOutput.Output} {
		data, err := json.Marshal(item)
		if err != nil || !strings.Contains(string(data), "9007199254740993") {
			t.Fatalf("lost precision: %s (%v)", data, err)
		}
	}
	for _, invalid := range []string{`true`, `[null]`, `[{"type":"unknown"}]`, `[{"type":"message","role":"user","content":42}]`, `[{"type":"function_call_output","output":true}]`, `[{"type":"message","type":"function_call"}]`} {
		if err := jsonutil.Unmarshal([]byte(invalid), &value); err == nil {
			t.Errorf("accepted %s", invalid)
		}
	}
}

func TestResponsesRejectMalformedBodiesV2(t *testing.T) {
	for _, body := range []string{`{"model":"test","input":"hello"} {}`, `{"model":"test","input":"hello"} garbage`, `{"model":"first","model":"second","input":"hello"}`, "{\"model\":\"test\",\"input\":\"\xff\"}"} {
		rec := httptest.NewRecorder()
		New(&config.Config{}).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("accepted %q: %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestResponseEventsRejectInvalidJSONV2(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := writeEvent(rec, "response.created", map[string]any{"text": string([]byte{0xff})}); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("wrote partial SSE frame: %s", rec.Body.String())
	}
}

func BenchmarkResponsesInputV2(b *testing.B) {
	data := []byte(`[{"role":"user","content":[{"type":"input_text","text":"hello"}],"type":"message"},{"output":"done","call_id":"f","type":"function_call_output"}]`)
	b.ReportAllocs()
	for b.Loop() {
		var value ResponsesInput
		if err := jsonutil.Unmarshal(data, &value); err != nil {
			b.Fatal(err)
		}
	}
}

func TestOutputUnionsRoundTripV2(t *testing.T) {
	for _, input := range []string{
		`{"content":[{"type":"refusal","refusal":"no"}],"phase":null,"role":"assistant","status":"completed","id":"m","type":"message"}`,
		`{"content":[{"type":"output_text","text":"hi","annotations":[],"logprobs":[]}],"phase":"final_answer","role":"assistant","status":"completed","id":"m","type":"message"}`,
		`{"arguments":"{\"id\":9007199254740993}","call_id":"c","name":"lookup","namespace":"tools","async":true,"status":"completed","id":"f","type":"function_call"}`,
		`{"operation":{"type":"delete_file","path":"file"},"call_id":"c","status":"completed","id":"p","type":"apply_patch_call"}`,
		`{"action":{"commands":["pwd"]},"environment":{"type":"local"},"call_id":"c","status":"completed","id":"s","type":"shell_call"}`,
		`{"action":{"command":["pwd"]},"call_id":"c","status":"completed","id":"s","type":"local_shell_call"}`,
		`{"summary":[],"content":[],"encrypted_content":"opaque","status":"completed","id":"r","type":"reasoning"}`,
		`{"content":"summary","encrypted_content":"opaque","id":"c","type":"compaction"}`,
		`{"arguments":{"id":9007199254740993},"call_id":null,"execution":"server","status":"completed","id":"t","type":"tool_search_call"}`,
		`{"tools":[{"type":"function","name":"lookup","parameters":{"enum":[1e400]}}],"call_id":null,"execution":"server","status":"completed","id":"t","type":"tool_search_output"}`,
		`{"actions":[{"value":1e400}],"pending_safety_checks":[],"status":"completed","call_id":"c","id":"c","type":"computer_call"}`,
		`{"input":"raw","name":"exec","status":"completed","call_id":"c","id":"c","type":"custom_tool_call"}`,
	} {
		var output ResponseOutput
		if err := jsonutil.Unmarshal([]byte(input), &output); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		if err := jsonutil.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if err := jsonutil.Unmarshal([]byte(input), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("output union changed:\n got %s\nwant %s", data, input)
		}
	}
	for _, input := range []string{`[]`, `{"type":"future_unknown"}`, `{"type":"message","content":42}`} {
		if err := json.Unmarshal([]byte(input), new(ResponseOutput)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func TestOutputDiscriminatorV2(t *testing.T) {
	for _, output := range []ResponseOutput{
		{Type: ResponseOutputTypeFunctionCall, FunctionCallOutputItem: &FunctionCallOutputItem{}},
		{Type: ResponseOutputTypeComputerCall, ComputerCallItem: &ComputerCallItem{}},
		{Type: ResponseOutputTypeApplyPatchCall, ApplyPatchCallItem: &ApplyPatchCallItem{}},
		{Type: ResponseOutputTypeCustomToolCall, CustomToolCallItem: &CustomToolCallItem{}},
		{Type: ResponseOutputTypeShellCall, ShellCallItem: &ShellCallItem{}},
		{Type: ResponseOutputTypeLocalShellCall, ShellCallItem: &ShellCallItem{}},
		{Type: ResponseOutputTypeReasoning, ReasoningOutputItem: &ReasoningOutputItem{}},
		{Type: ResponseOutputTypeCompaction, CompactionOutputItem: &CompactionOutputItem{}},
	} {
		data, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ResponseOutput
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		if decoded.Type != output.Type {
			t.Errorf("got %s, want %s", decoded.Type, output.Type)
		}
	}
}

func TestToolSearchOutputRequiresPayloadV2(t *testing.T) {
	if _, err := json.Marshal(ResponseOutput{Type: ResponseOutputTypeToolSearchOutput}); err == nil {
		t.Fatal("accepted tool-search output without its payload")
	}
}

func TestComputerOutputEmptyArraysV2(t *testing.T) {
	data, err := json.Marshal(ResponseOutput{
		Type:             ResponseOutputTypeComputerCall,
		ComputerCallItem: &ComputerCallItem{ID: "computer", CallID: "call", Status: "completed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"actions", "pending_safety_checks"} {
		if values, ok := fields[name].([]any); !ok || len(values) != 0 {
			t.Errorf("%s must be an empty array: %s", name, data)
		}
	}
}
