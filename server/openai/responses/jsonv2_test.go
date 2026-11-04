package responses

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
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
