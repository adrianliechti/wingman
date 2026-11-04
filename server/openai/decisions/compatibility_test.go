package decisions

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func requestFor(t *testing.T, input, questions string) Request {
	t.Helper()
	var r Request
	if err := json.Unmarshal([]byte(`{"model":"m","input":`+input+`,"questions":`+questions+`}`), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDecisionsDocumentedInputs(t *testing.T) {
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("image"))
	}))
	defer image.Close()
	for name, input := range map[string]string{
		"empty string": `""`,
		"json object":  `[{"role":"user","content":[{"type":"input_json","data":{"id":9007199254740993,"role":"system","image_url":"ordinary data"}}]}]`,
		"json array":   `[{"role":"user","content":[{"type":"input_json","data":[{"x":1},{"x":2}]}]}]`,
		"hosted image": fmt.Sprintf(`[{"role":"user","content":[{"type":"input_image","image_url":%q,"detail":"original"}]}]`, image.URL),
		"history":      `[{"role":"system","content":[{"type":"input_text","text":"rules","prompt_cache_breakpoint":{"mode":"explicit"}}]},{"role":"developer","content":"more rules"},{"role":"user","content":"help"},{"role":"assistant","content":[{"type":"output_text","text":""},{"type":"refusal","refusal":"no"}]},{"type":"function_call","call_id":"f","name":"lookup","arguments":"{}"},{"type":"custom_tool_call","call_id":"c","name":"code","input":"hello"},{"type":"function_call_output","call_id":"f","output":"done"},{"type":"custom_tool_call_output","call_id":"c","output":[{"type":"input_text","text":"done","prompt_cache_breakpoint":{"mode":"explicit"}}]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			r := requestFor(t, input, `[{"type":"predicate","instructions":"check"}]`)
			if _, err := r.ProviderInput(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, typ := range []string{"predicate", "choice", "score"} {
		extra := ""
		if typ == "choice" {
			extra = `,"choices":[{"value":false},{"value":"false"}]`
		}
		if typ == "score" {
			extra = `,"levels":[{"label":"low"},{"label":"high"}]`
		}
		r := requestFor(t, `"shared"`, fmt.Sprintf(`[{"type":%q,"instructions":[{"type":"input_text","text":"compare"},{"type":"input_image","image_url":%q}]%s}]`, typ, image.URL, extra))
		if _, err := r.ProviderInput(); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
}

func TestDecisionsInvalidContentAndHistory(t *testing.T) {
	for name, input := range map[string]string{
		"bad role":          `[{"role":"tool","content":"x"}]`,
		"assistant input":   `[{"role":"assistant","content":[{"type":"input_text","text":"x"}]}]`,
		"user output":       `[{"role":"user","content":[{"type":"output_text","text":"x"}]}]`,
		"user refusal":      `[{"role":"user","content":[{"type":"refusal","refusal":"x"}]}]`,
		"system json":       `[{"role":"system","content":[{"type":"input_json","data":{}}]}]`,
		"system image":      `[{"role":"system","content":[{"type":"input_image","image_url":"data:image/png;base64,aGk="}]}]`,
		"json scalar":       `[{"role":"user","content":[{"type":"input_json","data":1}]}]`,
		"json scalar array": `[{"role":"user","content":[{"type":"input_json","data":[1]}]}]`,
		"missing image":     `[{"role":"user","content":[{"type":"input_image"}]}]`,
		"file id":           `[{"role":"user","content":[{"type":"input_image","file_id":"f"}]}]`,
		"two image sources": `[{"role":"user","content":[{"type":"input_image","file_id":"f","image_url":"data:image/png;base64,aGk="}]}]`,
		"nonimage data":     `[{"role":"user","content":[{"type":"input_image","image_url":"data:text/plain;base64,aGk="}]}]`,
		"wrong cache mode":  `[{"role":"user","content":[{"type":"input_text","text":"x","prompt_cache_breakpoint":{"mode":"implicit"}}]}]`,
		"json cache":        `[{"role":"user","content":[{"type":"input_json","data":{},"prompt_cache_breakpoint":{"mode":"explicit"}}]}]`,
		"missing call":      `[{"type":"function_call_output","call_id":"f","output":"x"}]`,
		"missing args":      `[{"type":"function_call","call_id":"f","name":"lookup"}]`,
		"duplicate call":    `[{"type":"function_call","call_id":"f","name":"lookup","arguments":""},{"type":"function_call","call_id":"f","name":"lookup","arguments":""}]`,
		"wrong output kind": `[{"type":"function_call","call_id":"f","name":"lookup","arguments":""},{"type":"custom_tool_call_output","call_id":"f","output":"x"}]`,
		"duplicate output":  `[{"type":"function_call","call_id":"f","name":"lookup","arguments":""},{"type":"function_call_output","call_id":"f","output":"x"},{"type":"function_call_output","call_id":"f","output":"x"}]`,
		"unknown item":      `[{"type":"item_reference","id":"x"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			r := requestFor(t, input, `[{"type":"predicate","instructions":"check"}]`)
			if _, err := r.ProviderInput(); err == nil {
				t.Fatal("accepted invalid content")
			}
		})
	}
}

func TestDecisionsRequestLimits(t *testing.T) {
	for _, n := range []int{1, 400, 401} {
		r := requestFor(t, `"x"`, `[`+strings.TrimSuffix(strings.Repeat(`{"type":"predicate","instructions":"x"},`, n), ",")+`]`)
		_, err := r.ProviderInput()
		if (err == nil) != (n <= 400) {
			t.Fatalf("%d questions: %v", n, err)
		}
	}
	image := `{"type":"input_image","image_url":"data:image/png;base64,aGk="}`
	for _, n := range []int{127, 128} {
		r := requestFor(t, `[{"role":"user","content":[`+strings.TrimSuffix(strings.Repeat(image+",", n), ",")+`]}]`, `[{"type":"predicate","instructions":[`+image+`]}]`)
		_, err := r.ProviderInput()
		if (err == nil) != (n == 127) {
			t.Fatalf("combined image count %d: %v", n+1, err)
		}
	}
	for _, data := range []string{`{"x":"` + strings.Repeat("x", 256*1024) + `"}`, strings.Repeat(`{"x":`, 33) + `0` + strings.Repeat("}", 33), `{"x":[` + strings.Repeat(`0,`, 10000) + `0]}`} {
		r := requestFor(t, `[{"role":"user","content":[{"type":"input_json","data":`+data+`}]}]`, `[{"type":"predicate","instructions":"x"}]`)
		if _, err := r.ProviderInput(); err == nil {
			t.Fatal("accepted oversized JSON")
		}
	}
}

func TestDecisionsQuestionValidation(t *testing.T) {
	for _, questions := range []string{
		`[{"type":"predicate","instructions":[{"type":"input_text","text":"x","prompt_cache_breakpoint":{"mode":"explicit"}}]}]`,
		`[{"type":"predicate","instructions":"x","choices":[{"value":"a"}]}]`,
		`[{"type":"choice","instructions":"x","choices":[{"value":null}]}]`,
		`[{"type":"score","instructions":"x","levels":[{"value":1}]}]`,
		`[{"type":"score","instructions":"x","levels":[{"value":1},{"value":1}]}]`,
		`[{"type":"predicate","id":"x","instructions":"x"},{"type":"predicate","id":"x","instructions":"x"}]`,
	} {
		r := requestFor(t, `"x"`, questions)
		if _, err := r.ProviderInput(); err == nil {
			t.Fatalf("accepted %s", questions)
		}
	}
	r := requestFor(t, `"x"`, `[{"type":"predicate","instructions":"x"}]`)
	if err := json.Unmarshal([]byte(`{"mode":"explicit","ttl":"12h"}`), &r.PromptCacheOptions); err != nil {
		t.Fatal(err)
	}
	r.PromptCacheKey = new("tenant")
	input, err := r.ProviderInput()
	if err != nil || input.CacheOptions.Key != "tenant" {
		t.Fatalf("cache options: %+v %v", input, err)
	}
}

func TestDecisionsCacheValidation(t *testing.T) {
	for _, options := range []string{`{"mode":"unknown"}`, `{"ttl":"24h"}`} {
		r := requestFor(t, `"x"`, `[{"type":"predicate","instructions":"x"}]`)
		if err := json.Unmarshal([]byte(options), &r.PromptCacheOptions); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ProviderInput(); err == nil {
			t.Fatalf("accepted cache options %s", options)
		}
	}
	for _, n := range []int{64, 65} {
		r := requestFor(t, `"x"`, `[{"type":"predicate","instructions":"x"}]`)
		r.PromptCacheKey = new(strings.Repeat("é", n))
		_, err := r.ProviderInput()
		if (err == nil) != (n == 64) {
			t.Fatalf("cache key length %d: %v", n, err)
		}
	}
}
