package responses

import (
	"encoding/json/v2"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
)

func TestRequestSchemaNumbers(t *testing.T) {
	const schema = `{"properties":{"id":{"enum":[9007199254740993,1.234567890123456789,1e400,null]}},"type":"object"}`
	c := &optionsCompleter{}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", c)
	body := `{"model":"test","input":"hi","tools":[{"type":"function","name":"lookup","parameters":` + schema + `}],"text":{"format":{"type":"json_schema","name":"answer","schema":` + schema + `}}}`
	rec := httptest.NewRecorder()
	New(cfg).handleResponses(rec, httptest.NewRequest("POST", "/responses", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var input ResponsesInput
	if err := json.Unmarshal([]byte(`[{"type":"additional_tools","role":"user","tools":[{"type":"function","name":"lookup","parameters":`+schema+`}]}]`), &input); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{c.options.Tools[0].Parameters, c.options.Schema.Properties, input.Items[0].InputAdditionalTools.Tools[0].Parameters} {
		data, err := json.Marshal(value, json.Deterministic(true))
		if err != nil || string(data) != schema {
			t.Fatalf("schema changed: %s (%v)", data, err)
		}
	}
}
