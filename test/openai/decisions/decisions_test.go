package decisions_test

import (
	"encoding/json/v2"
	"flag"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	provider "github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server"
	"github.com/adrianliechti/wingman/server/openai/decisions"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

var configPath = flag.String("decisions-config", "", "start a Wingman test server from this config for cross-model tests")

// Compare the native provider and HTTP mapping with a direct OpenAI request.
// An in-process Wingman server ensures this exercises the native decider even
// when the regular Wingman config registers the same model as a completer.
func TestNativeHTTP(t *testing.T) {
	h := openai.New(t)
	model := os.Getenv("TEST_DECISIONS_NATIVE_MODEL")
	if model == "" {
		model = "gpt-6-luna"
	}
	decider, err := provider.NewDecider(h.OpenAI.BaseURL, model, provider.WithToken(h.OpenAI.APIKey), provider.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterDecider("native-decisions", decider)
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(s)
	t.Cleanup(local.Close)
	ep := harness.Endpoint{Name: "wingman-native", BaseURL: local.URL + "/v1"}

	reference := postDecisions(t, h, h.OpenAI, model)
	actual := postDecisions(t, h, ep, "native-decisions")
	requireDecision(t, reference, true)
	requireDecision(t, actual, true)
	harness.CompareStructure(t, "decisions", reference.Body, actual.Body, harness.CompareOption{Rules: map[string]harness.FieldRule{
		"model":                                 harness.FieldNonEmpty,
		"answers.*.probability":                 harness.FieldType,
		"answers.*.confidence":                  harness.FieldType,
		"answers.*.score":                       harness.FieldType,
		"answers.*.probabilities.*.probability": harness.FieldType,
		"answers.*.probabilities.*.value":       harness.FieldType,
		"answers.*.probabilities.*.label":       harness.FieldType,
		"usage.input_tokens":                    harness.FieldType,
		"usage.output_tokens":                   harness.FieldType,
		"usage.total_tokens":                    harness.FieldType,
		"usage.input_tokens_details.*":          harness.FieldType,
		"usage.output_tokens_details.*":         harness.FieldType,
	}})
}

// Run the same contract and classification checks across enabled decision
// aliases. TEST_DECISIONS_MODELS overrides the decisions alias in config.yaml.
func TestCrossModelHTTP(t *testing.T) {
	h := openai.New(t)
	if *configPath != "" {
		cfg, err := config.Parse(*configPath)
		if err != nil {
			t.Fatal(err)
		}
		s, err := server.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		local := httptest.NewServer(s)
		t.Cleanup(local.Close)
		h.Wingman.BaseURL = local.URL + "/v1"
	}
	if harness.ConfiguredModels(h.Wingman.BaseURL, h.Wingman.APIKey) == nil {
		t.Skip("wingman not reachable — start it with task server")
	}
	var models []string
	if selected := os.Getenv("TEST_DECISIONS_MODELS"); selected != "" {
		for name := range strings.SplitSeq(selected, ",") {
			if name = strings.TrimSpace(name); name != "" {
				models = append(models, name)
			}
		}
	} else {
		models = []string{"decisions"}
	}
	if len(models) == 0 {
		t.Fatal("TEST_DECISIONS_MODELS contains no models")
	}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model)
			response := postDecisions(t, h, h.Wingman, model)
			requireDecision(t, response, !strings.Contains(strings.ToLower(model), "embedding"))
			t.Logf("%s: %s", model, response.RawBody)
		})
	}
}

func postDecisions(t *testing.T, h *openai.Harness, ep harness.Endpoint, model string) *harness.RawResponse {
	t.Helper()
	body := map[string]any{
		"model": model,
		"input": "Our checkout has returned HTTP 500 errors since 9am. Customers cannot complete purchases. This is a software outage blocking revenue.",
		"questions": []map[string]any{
			{"type": "predicate", "name": "is_bug", "instructions": "Is the customer reporting a software defect?"},
			{"type": "choice", "name": "team", "instructions": "Which team should handle this ticket?", "choices": []map[string]any{
				{"value": "billing", "description": "Invoices, refunds, and payment questions."},
				{"value": "bug", "description": "Software faults, HTTP errors, and broken checkout functionality."},
				{"value": "account", "description": "Login, password, and account profile changes."},
			}},
			{"type": "score", "name": "urgency", "instructions": "How urgent is this ticket?", "levels": []map[string]any{
				{"label": "routine", "description": "Can wait for the next release."},
				{"label": "soon", "description": "Should be fixed this week."},
				{"label": "immediate", "description": "A software outage prevents purchases and blocks revenue now."},
			}},
		},
	}
	response, err := h.Client.Post(t.Context(), ep, "/decisions", body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s returned %d: %s", ep.Name, response.StatusCode, response.RawBody)
	}
	return response
}

func requireDecision(t *testing.T, response *harness.RawResponse, checkPredicate bool) {
	t.Helper()
	var result decisions.Response
	if err := json.Unmarshal(response.RawBody, &result); err != nil {
		t.Fatal(err)
	}
	if result.Model == "" || len(result.Answers) != 3 || result.Usage.InputTokens <= 0 || result.Usage.OutputTokens < 0 ||
		result.Usage.TotalTokens != result.Usage.InputTokens+result.Usage.OutputTokens {
		t.Fatalf("missing model, answers, or usage: %s", response.RawBody)
	}
	for i, name := range []string{"is_bug", "team", "urgency"} {
		a := result.Answers[i]
		if a.Name == nil || *a.Name != name {
			t.Fatalf("answer %d lost question order/name: %+v", i, a)
		}
		if a.Type == "refusal" {
			t.Fatalf("benign question %q was refused", name)
		}
	}
	predicate := result.Answers[0]
	if predicate.Type != "predicate" {
		t.Fatalf("unexpected predicate answer: %+v", predicate)
	}
	requireProbability(t, predicate.Probability)
	if checkPredicate && *predicate.Probability <= 0.5 {
		t.Errorf("clear software outage not recognized: %v", *predicate.Probability)
	}

	choice := result.Answers[1]
	if choice.Type != "choice" || choice.Choice != "bug" || len(choice.Probabilities) != 3 {
		t.Fatalf("unexpected classification: %+v", choice)
	}
	requireProbability(t, choice.Confidence)
	choices := map[any]bool{"billing": true, "bug": true, "account": true}
	var total, selected float64
	for _, p := range choice.Probabilities {
		requireProbability(t, p.Probability)
		if _, ok := p.Value.(string); !ok {
			t.Fatalf("choice value lost its string type: %v", p.Value)
		}
		if !choices[p.Value] {
			t.Fatalf("unknown or repeated choice probability: %v", p.Value)
		}
		delete(choices, p.Value)
		total += *p.Probability
		if p.Value == choice.Choice {
			selected = *p.Probability
		}
	}
	if math.Abs(total-1) > 1e-5 {
		t.Errorf("choice probabilities sum to %v", total)
	}
	for _, p := range choice.Probabilities {
		if *p.Probability > selected {
			t.Errorf("selected choice has less probability than %v", p.Value)
		}
	}

	score := result.Answers[2]
	if score.Type != "score" || score.Score == nil || len(score.Probabilities) != 3 {
		t.Fatalf("unexpected score answer: %+v", score)
	}
	requireProbability(t, score.Confidence)
	total = 0
	var weighted float64
	seen := make(map[float64]bool)
	labels := []string{"routine", "soon", "immediate"}
	for _, p := range score.Probabilities {
		requireProbability(t, p.Probability)
		index, ok := p.Value.(float64)
		if !ok || index < 0 || index > 2 || index != math.Trunc(index) || seen[index] ||
			p.Label == nil || *p.Label != labels[int(index)] {
			t.Fatalf("invalid score level: %+v", p)
		}
		seen[index] = true
		total += *p.Probability
		weighted += index * *p.Probability
	}
	if math.Abs(total-1) > 1e-5 || math.IsNaN(*score.Score) || math.IsInf(*score.Score, 0) ||
		*score.Score < 0 || *score.Score > 2 || math.Abs(*score.Score-weighted) > 1e-5 {
		t.Errorf("score %v does not match distribution (sum %v, weighted %v)", *score.Score, total, weighted)
	}
}

func requireProbability(t *testing.T, p *float64) {
	t.Helper()
	if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p > 1 {
		t.Fatalf("invalid probability: %v", p)
	}
}
