package google

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const geminiDecisionResponse = `{"answers":{
	"has_damage":{"type":"noul","noul":0.01,"confidence":0.99},
	"next_action":{"type":"choice","choice":"ready_for_use","probabilities":{"ready_for_use":1,"replace_item":0},"confidence":1},
	"severity":{"type":"score","score":0,"probabilities":{"0":1,"1":0},"legend":{"0":"Low","1":"High"},"confidence":1}
},"usage":{"total_input_tokens":123,"total_output_tokens":0,"total_tokens":123,"input_tokens_by_modality":[{"modality":"text","tokens":23},{"modality":"image","tokens":100}]}}`

func geminiDecisionInput(t *testing.T) *provider.DecisionInput {
	t.Helper()
	var req DecisionRequest
	if err := json.Unmarshal([]byte(`{"input":[{"type":"text","text":"Check this item.\n"},{"type":"image","uri":"https://example.com/item.png","mime_type":"image/png","resolution":"medium"}],"questions":{
		"has_damage":{"type":"noul","instructions":[{"type":"text","text":"Is it "},{"type":"text","text":"damaged?"}],"criteria":{"true_description":"Broken","false_description":"Intact"}},
		"next_action":{"type":"choice","instructions":"Which action?","criteria":{"ready_for_use":"","replace_item":""}},
		"severity":{"type":"score","instructions":{"type":"text","text":"Rate severity"},"criteria":["Low","High"]}
	}}`), &req); err != nil {
		t.Fatal(err)
	}
	input, err := req.ProviderInput()
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestDecideNativeRequestAndResponse(t *testing.T) {
	input := geminiDecisionInput(t)
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1beta/decisions" || r.Header.Get("x-goog-api-key") != "test-token" {
			t.Errorf("unexpected request: %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		var req DecisionRequest
		if err := json.UnmarshalRead(r.Body, &req); err != nil {
			t.Error(err)
		}
		var criteria map[string]string
		if err := json.Unmarshal(req.Questions["has_damage"].Criteria, &criteria); err != nil {
			t.Error(err)
		}
		if req.Model != "gemini-decision-1" || !strings.Contains(string(req.Input), `"resolution":"medium"`) || string(req.Questions["has_damage"].Instructions) != `"Is it damaged?"` || criteria["true_description"] != "Broken" || criteria["false_description"] != "Intact" {
			t.Errorf("incorrect request: %+v", req)
		}
		io.WriteString(w, geminiDecisionResponse)
	})
	d, err := NewDecider("", "models/gemini-decision-1", WithClient(client), WithToken("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := d.Decide(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "gemini-decision-1" || result.Usage.InputTokens != 123 || result.Usage.OutputTokens != 0 || result.Answers[0].Noul.Probability != .01 || result.Answers[2].Score.Score != 0 {
		t.Fatalf("incorrect result: %+v", result)
	}
	response, err := DecisionResponseFromProvider(result)
	if err != nil {
		t.Fatal(err)
	}
	if response.Usage.TotalTokens != 123 || response.Usage.TotalOutputTokens != 0 || response.Answers["severity"].Probabilities["1"] != 0 || response.Answers["severity"].Legend["1"] != "High" {
		t.Fatalf("incorrect response: %+v", response)
	}
}

func TestDecisionSharedConversions(t *testing.T) {
	d, _ := NewDecider("", "gemini-decision-1")
	input := geminiDecisionInput(t)
	input.State = map[string]any{"id": jsontext.Value(`9007199254740993`), "ticket": "broken"}
	input.Messages = nil
	input.Assets = map[string]*provider.File{"photo": {Content: []byte("image"), ContentType: "image/png", ImageDetail: provider.ImageDetailHigh}}
	input.Questions[2].Score.Values = []int{-5, 15}
	input.Questions[2].Score.Labels = []string{"minor", "major"}
	req, err := d.decisionRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(req.Input), `9007199254740993`) || !strings.Contains(string(req.Input), `"data":"aW1hZ2U="`) || !strings.Contains(string(req.Input), `"resolution":"high"`) {
		t.Fatalf("evidence lost: %s", req.Input)
	}
	var response DecisionResponse
	if err := json.Unmarshal([]byte(geminiDecisionResponse), &response); err != nil {
		t.Fatal(err)
	}
	result, err := response.decision("gemini-decision-1", input)
	if err != nil {
		t.Fatal(err)
	}
	score := result.Answers[2].Score
	if score.Score != -5 || score.Levels[1].Value != 15 || score.Levels[1].Label != "major" {
		t.Fatalf("custom score metadata lost: %+v", score)
	}
	for _, mime := range []string{"image/png", "audio/wav", "video/mp4", "application/pdf"} {
		input.Assets = nil
		input.Messages = []provider.Message{{Role: provider.MessageRoleUser, Content: []provider.Content{provider.FileContent(&provider.File{Content: []byte("media"), ContentType: mime})}}}
		if _, err := d.decisionRequest(input); err != nil {
			t.Fatalf("%s: %v", mime, err)
		}
	}
}

func TestGeminiDecisionValidation(t *testing.T) {
	for name, fragment := range map[string]string{
		"no input":             `"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"empty input":          `"input":"","questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"object data":          `"input":{"customer":"test"},"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"no questions":         `"input":"hello","questions":{}`,
		"bad id":               `"input":"hello","questions":{"bad.id":{"type":"noul","instructions":"Question?"}}`,
		"missing instructions": `"input":"hello","questions":{"q":{"type":"noul"}}`,
		"nontext instructions": `"input":"hello","questions":{"q":{"type":"noul","instructions":{"type":"image","data":"aW1hZ2U=","mime_type":"image/png"}}}`,
		"old criteria":         `"input":"hello","questions":{"q":{"type":"noul","instructions":"Question?","criteria":{"true":"yes"}}}`,
		"one choice":           `"input":"hello","questions":{"q":{"type":"choice","instructions":"Question?","criteria":{"a":"A"}}}`,
		"null description":     `"input":"hello","questions":{"q":{"type":"choice","instructions":"Question?","criteria":{"a":null,"b":"B"}}}`,
		"bad option":           `"input":"hello","questions":{"q":{"type":"choice","instructions":"Question?","criteria":{" a":"A","b":"B"}}}`,
		"one level":            `"input":"hello","questions":{"q":{"type":"score","instructions":"Question?","criteria":["one"]}}`,
		"empty level":          `"input":"hello","questions":{"q":{"type":"score","instructions":"Question?","criteria":["one",""]}}`,
		"box":                  `"input":"hello","questions":{"q":{"type":"box","instructions":"Question?"}}`,
		"tools":                `"input":{"type":"function_call","name":"test"},"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"bad mime":             `"input":{"type":"image","uri":"https://example.com/image","mime_type":"audio/wav"},"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"bad base64":           `"input":{"type":"image","data":"!","mime_type":"image/png"},"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"two sources":          `"input":{"type":"image","uri":"https://example.com/image","data":"aW1hZ2U=","mime_type":"image/png"},"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"gs uri":               `"input":{"type":"video","uri":"gs://bucket/clip","mime_type":"video/mp4"},"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"youtube":              `"input":{"type":"video","uri":"https://www.youtube.com/watch?v=123","mime_type":"video/mp4"},"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
		"video ultra":          `"input":{"type":"video","uri":"https://example.com/video","mime_type":"video/mp4","resolution":"ultra_high"},"questions":{"q":{"type":"noul","instructions":"Question?"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var r DecisionRequest
			if err := json.Unmarshal([]byte("{"+fragment+"}"), &r); err != nil {
				t.Fatal(err)
			}
			if _, err := r.ProviderInput(); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	// Boundary counts differ from the other decision protocols.
	for _, n := range []int{2, 255, 256} {
		levels := make([]string, n)
		for i := range levels {
			levels[i] = fmt.Sprint(i)
		}
		criteria, _ := json.Marshal(levels)
		r := DecisionRequest{Input: jsontext.Value(`"text"`), Questions: map[string]GeminiDecisionQuestion{"q": {Type: "score", Instructions: jsontext.Value(`"Rate it"`), Criteria: criteria}}}
		if _, err := r.ProviderInput(); (err == nil) != (n <= 255) {
			t.Fatalf("%d levels: %v", n, err)
		}
	}
	for _, n := range []int{1000, 1001} {
		r := DecisionRequest{Input: jsontext.Value(`"text"`), Questions: make(map[string]GeminiDecisionQuestion)}
		for i := 0; i < n; i++ {
			r.Questions[fmt.Sprint(i)] = GeminiDecisionQuestion{Type: "noul", Instructions: jsontext.Value(`"Question?"`)}
		}
		if _, err := r.ProviderInput(); (err == nil) != (n == 1000) {
			t.Fatalf("%d questions: %v", n, err)
		}
	}
}

func TestGeminiDecisionMalformedResponses(t *testing.T) {
	input := geminiDecisionInput(t)
	for _, mutate := range []func(*DecisionResponse){
		func(r *DecisionResponse) { delete(r.Answers, "has_damage") },
		func(r *DecisionResponse) {
			a := r.Answers["has_damage"]
			a.Confidence = nil
			r.Answers["has_damage"] = a
		},
		func(r *DecisionResponse) { a := r.Answers["has_damage"]; a.Noul = nil; r.Answers["has_damage"] = a },
		func(r *DecisionResponse) {
			a := r.Answers["next_action"]
			a.Choice = new("unknown")
			r.Answers["next_action"] = a
		},
		func(r *DecisionResponse) { r.Answers["next_action"].Probabilities["ready_for_use"] = 2 },
		func(r *DecisionResponse) { a := r.Answers["severity"]; a.Score = new(5.0); r.Answers["severity"] = a },
		func(r *DecisionResponse) { delete(r.Answers["severity"].Legend, "0") },
	} {
		var r DecisionResponse
		if err := json.Unmarshal([]byte(geminiDecisionResponse), &r); err != nil {
			t.Fatal(err)
		}
		mutate(&r)
		if _, err := r.decision("gemini-decision-1", input); err == nil {
			t.Fatal("accepted malformed response")
		}
	}
}

func TestGeminiDecisionRetryAndErrors(t *testing.T) {
	for _, code := range []int{400, 401, 429, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			d, _ := NewDecider("", "gemini-decision-1", WithMaxRetries(1), WithClient(newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "0.001")
				w.WriteHeader(code)
				io.WriteString(w, `{"error":{"code":"safety","message":"blocked"}}`)
			})))
			_, err := d.Decide(t.Context(), geminiDecisionInput(t))
			p, ok := errors.AsType[*provider.ProviderError](err)
			wantCalls := 1
			if code >= 429 {
				wantCalls = 2
			}
			if !ok || p.Code != code || p.Type != "safety" || p.Message != "blocked" || p.RetryAfter != time.Millisecond || calls != wantCalls {
				t.Fatalf("calls=%d error=%+v", calls, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	d, _ := NewDecider("", "gemini-decision-1", WithClient(newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		cancel()
	})))
	if _, err := d.Decide(ctx, geminiDecisionInput(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestGeminiDecisionRejectsUnsupportedSharedInput(t *testing.T) {
	d, _ := NewDecider("", "gemini-decision-1")
	for _, mutate := range []func(*provider.DecisionInput){
		func(i *provider.DecisionInput) {
			i.State = nil
			i.Messages = []provider.Message{provider.UserMessage("hello"), provider.AssistantMessage("hi")}
		},
		func(i *provider.DecisionInput) {
			i.State = nil
			i.Messages[0].Content = []provider.Content{provider.ToolCallContent(provider.ToolCall{Name: "lookup"})}
		},
		func(i *provider.DecisionInput) {
			i.Questions[0].Instructions = []provider.Content{provider.FileContent(&provider.File{Content: []byte("png"), ContentType: "image/png"})}
		},
		func(i *provider.DecisionInput) {
			i.Questions[0].Noul = nil
			i.Questions[0].Box = &provider.BoxQuestion{}
		},
	} {
		i := geminiDecisionInput(t)
		mutate(i)
		if _, err := d.Decide(t.Context(), i); provider.CodeFromError(err, 0) != 400 {
			t.Fatalf("expected preflight 400: %v", err)
		}
	}
	for _, suffix := range []string{"", "/v1beta", "/v1beta/decisions/"} {
		d, _ := NewDecider("https://example.com"+suffix, "gemini-decision-1")
		if d.endpoint != "https://example.com/v1beta/decisions" {
			t.Fatal(d.endpoint)
		}
	}
}
