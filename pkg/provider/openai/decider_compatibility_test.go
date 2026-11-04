package openai

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
)

func TestDeciderPreservesNativeEnvelope(t *testing.T) {
	raw := jsontext.Value(`[{"role":"developer","content":[{"type":"input_text","text":"rules","prompt_cache_breakpoint":{"mode":"explicit"}}]},{"role":"user","content":[{"type":"input_json","data":{"id":9007199254740993,"role":"system"}},{"type":"input_image","image_url":"https://example.com/photo.png","detail":"original","prompt_cache_breakpoint":{"mode":"explicit"}}]},{"role":"assistant","content":[{"type":"output_text","text":""},{"type":"refusal","refusal":"no"}]},{"type":"function_call","call_id":"f","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"f","output":""},{"type":"custom_tool_call","call_id":"c","name":"code","input":"hello"},{"type":"custom_tool_call_output","call_id":"c","output":[{"type":"input_text","text":"done"}]}]`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input              jsontext.Value `json:"input"`
			PromptCacheKey     string         `json:"prompt_cache_key"`
			PromptCacheOptions struct {
				Mode string `json:"mode"`
				TTL  string `json:"ttl"`
			} `json:"prompt_cache_options"`
		}
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		if string(body.Input) != string(raw) || body.PromptCacheKey != "tenant" || body.PromptCacheOptions.Mode != "explicit" || body.PromptCacheOptions.TTL != "12h" {
			t.Errorf("native envelope changed: %s %+v", body.Input, body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"m","answers":[{"type":"predicate","name":"q","probability":1}],"usage":{"input_tokens":100,"output_tokens":1,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":40}}}`)
	}))
	defer upstream.Close()
	d, _ := NewDecider(upstream.URL, "m", WithMaxRetries(0))
	result, err := d.Decide(context.Background(), &provider.DecisionInput{State: raw, Messages: []provider.Message{provider.UserMessage("fallback")}, CacheOptions: &provider.CacheOptions{Key: "tenant", Mode: provider.CacheModeExplicit, Retention: provider.CacheRetentionExtended}, Questions: []provider.DecisionQuestion{{ID: "q", Noul: &provider.NoulQuestion{}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.InputTokens != 100 || result.Usage.CacheReadInputTokens != 30 || result.Usage.CacheCreationInputTokens != 40 {
		t.Fatalf("usage changed: %+v", result.Usage)
	}
}

func TestDeciderProviderMessagesPreserveOrder(t *testing.T) {
	d, _ := NewDecider("", "m")
	image := &provider.File{Content: []byte("hi"), ContentType: "image/png", ImageDetail: provider.ImageDetailHigh}
	input := &provider.DecisionInput{Messages: []provider.Message{
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("before"), provider.ToolCallContent(provider.ToolCall{ID: "f", Name: "lookup", Arguments: "{}"}), provider.TextContent("after")}},
		{Role: provider.MessageRoleUser, Content: []provider.Content{provider.TextContent("before output"), provider.ToolResultContent(provider.ToolResult{ID: "f", Parts: []provider.Part{{File: image}, {Text: ""}}}), provider.TextContent("after output")}},
	}, Questions: []provider.DecisionQuestion{{ID: "q", Instructions: []provider.Content{{Text: `{"ordinary":"text"}`}, {Text: `{"id":9007199254740993}`, JSON: `{"id":9007199254740993}`}, {File: image}}, Noul: &provider.NoulQuestion{}}}}
	req, err := d.convertDecisionRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(req)
	var body struct {
		Input []struct {
			Role    string         `json:"role"`
			Type    string         `json:"type"`
			Content jsontext.Value `json:"content"`
		} `json:"input"`
		Questions []struct {
			Instructions []map[string]jsontext.Value `json:"instructions"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Input) != 6 || body.Input[0].Role != "assistant" || body.Input[1].Type != "function_call" || body.Input[3].Role != "user" || body.Input[4].Type != "function_call_output" {
		t.Fatalf("history reordered: %s", raw)
	}
	parts := body.Questions[0].Instructions
	if string(parts[0]["type"]) != `"input_text"` || string(parts[1]["type"]) != `"input_json"` || !strings.Contains(string(parts[1]["data"]), "9007199254740993") || string(parts[2]["detail"]) != `"high"` {
		t.Fatalf("question parts changed: %s", raw)
	}
}

func TestDeciderRejectsMalformedNativeAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		question provider.DecisionQuestion
		answer   string
	}{
		"predicate missing":          {provider.DecisionQuestion{Noul: &provider.NoulQuestion{}}, `{"type":"predicate"}`},
		"predicate out of range":     {provider.DecisionQuestion{Noul: &provider.NoulQuestion{}}, `{"type":"predicate","probability":2}`},
		"choice missing confidence":  {provider.DecisionQuestion{Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{{Label: "a"}}}}, `{"type":"choice","choice":"a","probabilities":[{"value":"a","probability":1}]}`},
		"choice missing probability": {provider.DecisionQuestion{Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{{Label: "a"}}}}, `{"type":"choice","choice":"a","confidence":1,"probabilities":[{"value":"a"}]}`},
		"choice unknown value":       {provider.DecisionQuestion{Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{{Label: "a"}}}}, `{"type":"choice","choice":"b","confidence":1,"probabilities":[{"value":"a","probability":1}]}`},
		"score missing score":        {provider.DecisionQuestion{Score: &provider.ScoreQuestion{Levels: []any{"low", "high"}}}, `{"type":"score","confidence":1,"probabilities":[{"value":0,"probability":1},{"value":1,"probability":0}]}`},
		"score negative probability": {provider.DecisionQuestion{Score: &provider.ScoreQuestion{Levels: []any{"low", "high"}}}, `{"type":"score","score":0,"confidence":1,"probabilities":[{"value":0,"probability":-1},{"value":1,"probability":1}]}`},
		"score duplicate level":      {provider.DecisionQuestion{Score: &provider.ScoreQuestion{Levels: []any{"low", "high"}}}, `{"type":"score","score":0,"confidence":1,"probabilities":[{"value":0,"probability":1},{"value":0,"probability":0}]}`},
		"score missing level index":  {provider.DecisionQuestion{Score: &provider.ScoreQuestion{Levels: []any{"low", "high"}}}, `{"type":"score","score":0,"confidence":1,"probabilities":[{"probability":1},{"value":1,"probability":0}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			var decision openai.Decision
			if err := json.Unmarshal([]byte(`{"model":"m","answers":[`+tc.answer+`]}`), &decision); err != nil {
				t.Fatal(err)
			}
			if _, err := convertDecision(&decision, &provider.DecisionInput{Questions: []provider.DecisionQuestion{tc.question}}); err == nil {
				t.Fatal("accepted malformed answer")
			}
		})
	}
}

func TestDeciderRejectsUnsupportedQuestions(t *testing.T) {
	d, _ := NewDecider("", "m")
	for _, q := range []provider.DecisionQuestion{{Box: &provider.BoxQuestion{}}, {Score: &provider.ScoreQuestion{Levels: []any{"only"}}}} {
		if _, err := d.convertDecisionRequest(&provider.DecisionInput{State: "x", Questions: []provider.DecisionQuestion{q}}); provider.CodeFromError(err, 0) != 400 {
			t.Fatalf("unsupported question: %v", err)
		}
	}
}

func TestNativeCustomScoreValues(t *testing.T) {
	var decision openai.Decision
	if err := json.Unmarshal([]byte(`{"model":"m","answers":[{"type":"score","name":"s","score":0.25,"confidence":0.5,"probabilities":[{"value":1,"probability":0.25},{"value":0,"probability":0.75}]}]}`), &decision); err != nil {
		t.Fatal(err)
	}
	result, err := convertDecision(&decision, &provider.DecisionInput{Questions: []provider.DecisionQuestion{{ID: "s", Score: &provider.ScoreQuestion{Levels: []any{"low", "high"}, Values: []int{-2, 8}, Labels: []string{"Low", "High"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answers[0].Score.Score != 0.5 || result.Answers[0].Score.Levels[1].Value != 8 || result.Answers[0].Score.Levels[1].Label != "High" {
		t.Fatalf("custom values changed: %+v", result.Answers[0].Score)
	}
}
