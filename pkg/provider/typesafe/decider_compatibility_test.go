package typesafe

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestSystemOneDocumentedAssetsAndMixedQuestions(t *testing.T) {
	for _, confidence := range []string{"null", "0", "0.9"} {
		t.Run(confidence, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("TypeSafe-Danger-Zone-Images") != "true" {
					t.Error("missing images header")
				}
				var body decisionRequest
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Error(err)
				}
				if body.State != "photo" || len(body.Assets) != 1 || body.Assets["photo"].DataBase64 != "aGk=" || body.Assets["photo"].Format != "png" || body.Questions["p"].Criteria.(map[string]any)["true"] != "contains photo" {
					t.Errorf("lost assets/criteria: %+v", body)
				}
				io.WriteString(w, `{"model":"jev-preview","answers":{"p":{"type":"noul","noul":0},"s":{"type":"score","score":0,"confidence":1,"legend":{"0":"only"},"probabilities":{"0":1}},"b":{"type":"box","box":{"top":0.2,"bottom":0.66,"left":0.19,"right":0.68},"confidence":`+confidence+`}},"usage":{"input_tokens":285,"output_tokens":20}}`)
			}))
			defer upstream.Close()
			d, _ := NewDecider(upstream.URL, "jev-preview", WithMaxRetries(0))
			input := &provider.DecisionInput{State: "photo", Assets: map[string]*provider.File{"photo": {Content: []byte("hi"), ContentType: "image/png"}}, Questions: []provider.DecisionQuestion{
				{ID: "p", Noul: &provider.NoulQuestion{True: "contains photo"}},
				{ID: "s", Score: &provider.ScoreQuestion{Levels: []any{"only"}}},
				{ID: "b", Instructions: "Locate red rectangle", Box: &provider.BoxQuestion{}},
			}}
			result, err := d.Decide(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if result.Answers[0].Noul.Probability != 0 || result.Answers[1].Score.Score != 0 || result.Answers[2].Box.Box.Left != 0.19 || (result.Answers[2].Box.Confidence == nil) != (confidence == "null") {
				t.Fatalf("changed answers: %+v", result)
			}
		})
	}
}

func TestSystemOneMessagesTakePrecedenceAndCreateAssets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body decisionRequest
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "stale") || strings.Contains(string(raw), `"source"`) || len(body.Assets) != 3 || !strings.Contains(string(raw), "tool_output") || r.Header.Get("TypeSafe-Danger-Zone-Images") != "true" {
			t.Errorf("bad image conversion: %s", raw)
		}
		for _, a := range body.Assets {
			if a.Type != "image" || a.Format != "jpeg" || a.DataBase64 != "aGk=" {
				t.Errorf("bad asset: %+v", a)
			}
		}
		io.WriteString(w, `{"model":"m","answers":{"q":{"type":"noul","noul":1}},"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()
	image := &provider.File{Content: []byte("hi"), ContentType: "image/jpeg"}
	input := &provider.DecisionInput{State: "stale", Messages: []provider.Message{
		{Role: provider.MessageRoleUser, Content: []provider.Content{provider.FileContent(image)}},
		{Role: provider.MessageRoleUser, Content: []provider.Content{provider.ToolResultContent(provider.ToolResult{ID: "f", Parts: []provider.Part{{File: image}}})}},
	}, Questions: []provider.DecisionQuestion{{ID: "q", Instructions: []provider.Content{provider.TextContent("compare"), provider.FileContent(image)}, Noul: &provider.NoulQuestion{}}}}
	d, _ := NewDecider(upstream.URL, "m", WithMaxRetries(0))
	if _, err := d.Decide(context.Background(), input); err != nil {
		t.Fatal(err)
	}
}

func TestSystemOneRejectsMalformedBoxes(t *testing.T) {
	for _, answer := range []string{
		`{"type":"box","confidence":null}`,
		`{"type":"box","box":{"top":0,"bottom":1,"left":0},"confidence":null}`,
		`{"type":"box","box":{"top":-0.1,"bottom":1,"left":0,"right":1},"confidence":null}`,
		`{"type":"box","box":{"top":0.8,"bottom":0.2,"left":0,"right":1},"confidence":null}`,
		`{"type":"box","box":{"top":0,"bottom":1,"left":0.9,"right":0.1},"confidence":null}`,
		`{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1},"confidence":2}`,
		`{"type":"box","boxes":[]}`,
		`{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1}}`,
	} {
		var r decisionResponse
		if err := json.Unmarshal([]byte(`{"model":"m","answers":{"q":`+answer+`}}`), &r); err != nil {
			continue
		}
		if _, err := r.decision(&provider.DecisionInput{Questions: []provider.DecisionQuestion{{ID: "q", Box: &provider.BoxQuestion{}}}}); err == nil {
			t.Fatalf("accepted %s", answer)
		}
	}
}

func TestSystemOneRejectsIncompatibleImageFormats(t *testing.T) {
	d, _ := NewDecider("http://invalid.invalid", "m", WithMaxRetries(0))
	for _, input := range []*provider.DecisionInput{
		{State: "photo", Assets: map[string]*provider.File{"photo": {Content: []byte("hi"), ContentType: "image/webp"}}, Questions: []provider.DecisionQuestion{{Noul: &provider.NoulQuestion{}}}},
		{Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: []provider.Content{{File: &provider.File{Content: []byte("hi"), ContentType: "image/gif"}}}}}, Questions: []provider.DecisionQuestion{{Noul: &provider.NoulQuestion{}}}},
	} {
		if _, err := d.Decide(context.Background(), input); provider.CodeFromError(err, 0) != 400 {
			t.Fatalf("format validation failed: %v", err)
		}
	}
}

func TestSystemOneCustomScoreValues(t *testing.T) {
	var response decisionResponse
	if err := json.Unmarshal([]byte(`{"model":"m","answers":{"s":{"type":"score","score":0.25,"confidence":0.5,"probabilities":{"0":0.75,"1":0.25},"legend":{"0":"low","1":{"id":9007199254740993}}}}}`), &response); err != nil {
		t.Fatal(err)
	}
	result, err := response.decision(&provider.DecisionInput{Questions: []provider.DecisionQuestion{{ID: "s", Score: &provider.ScoreQuestion{Levels: []any{"low", "high"}, Values: []int{-2, 8}, Labels: []string{"Low", "High"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	score := result.Answers[0].Score
	raw, _ := json.Marshal(score.Levels[1].Description)
	if score.Score != 0.5 || score.Levels[1].Value != 8 || score.Levels[1].Label != "High" || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("custom score changed: %+v", score)
	}
}
