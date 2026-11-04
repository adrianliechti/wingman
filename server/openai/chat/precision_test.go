package chat

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
	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":` + schema + `}}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":` + schema + `}}}`
	rec := httptest.NewRecorder()
	New(cfg).handleChatCompletion(rec, httptest.NewRequest("POST", "/chat/completions", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	for _, value := range []any{c.options.Tools[0].Parameters, c.options.Schema.Properties} {
		data, err := json.Marshal(value, json.Deterministic(true))
		if err != nil || string(data) != schema {
			t.Fatalf("schema changed: %s (%v)", data, err)
		}
	}
}
