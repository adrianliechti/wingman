package systemone_test

import (
	"encoding/json/v2"
	"flag"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server"
	"github.com/adrianliechti/wingman/test/harness"

	"github.com/joho/godotenv"
)

var configPath = flag.String("systemone-config", "", "start a Wingman test server from this config for live backend tests")

func TestSystemOneHTTP(t *testing.T) {
	loadDotenv()
	ep := harness.Endpoint{
		Name: "wingman", BaseURL: strings.TrimRight(env("WINGMAN_BASE_URL", "http://localhost:4242/v1"), "/"),
		APIKey: env("WINGMAN_API_KEY", "test-key"),
	}
	if *configPath != "" {
		cfg, err := config.Parse(*configPath)
		if err != nil {
			t.Fatal(err)
		}
		s, err := server.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		testServer := httptest.NewServer(s)
		t.Cleanup(testServer.Close)
		ep.BaseURL = testServer.URL + "/v1"
	}
	if harness.ConfiguredModels(ep.BaseURL, ep.APIKey) == nil {
		t.Skip("wingman not reachable — start it with task server")
	}
	for _, model := range []string{"nimble", "jev-1.13"} {
		t.Run(model, func(t *testing.T) {
			harness.SkipUnlessConfigured(t, ep.BaseURL, ep.APIKey, model)
			levels := []string{"Can wait for the next release", "Should be fixed this week", "Blocking revenue right now"}
			body := map[string]any{
				"model": model,
				"state": map[string]any{
					"customer_tier": "enterprise",
					"ticket":        "Our checkout has returned 500 errors since 9am. Customers cannot complete purchases.",
				},
				"questions": map[string]any{
					"is_bug": map[string]any{
						"type": "noul", "instructions": "Is the customer reporting a software defect?",
						"criteria": map[string]any{
							"true":  "The customer describes broken or unexpected product behavior.",
							"false": "The customer is asking a question or requesting a feature.",
						},
					},
					"label": map[string]any{
						"type": "choice", "instructions": "Which label fits this ticket?",
						"criteria": map[string]any{"billing": nil, "bug": nil, "account": nil},
					},
					"urgency": map[string]any{
						"type": "score", "instructions": "How urgent is this ticket?", "criteria": levels,
					},
				},
			}
			resp, err := harness.NewClient().Post(t.Context(), ep, "/systemone", body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("systemone returned %d: %s", resp.StatusCode, resp.RawBody)
			}
			var result struct {
				Model   string `json:"model"`
				Answers map[string]struct {
					Type          string             `json:"type"`
					Noul          *float64           `json:"noul"`
					Choice        *string            `json:"choice"`
					Score         *float64           `json:"score"`
					Confidence    *float64           `json:"confidence"`
					Probabilities map[string]float64 `json:"probabilities"`
					Legend        map[string]string  `json:"legend"`
				} `json:"answers"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(resp.RawBody, &result); err != nil {
				t.Fatal(err)
			}
			if result.Model == "" || result.Usage.InputTokens <= 0 || result.Usage.OutputTokens < 0 || len(result.Answers) != 3 {
				t.Fatalf("missing model, answers, or usage: %s", resp.RawBody)
			}
			bug := result.Answers["is_bug"]
			if bug.Type != "noul" || bug.Noul == nil {
				t.Fatalf("invalid noul answer: %+v", bug)
			}
			requireProbability(t, *bug.Noul)
			if *bug.Noul <= 0.5 {
				t.Errorf("clear outage not recognized as a software defect: %v", *bug.Noul)
			}
			label := result.Answers["label"]
			if label.Type != "choice" || label.Choice == nil || label.Confidence == nil {
				t.Fatalf("invalid choice answer: %+v", label)
			}
			requireProbability(t, *label.Confidence)
			requireDistribution(t, label.Probabilities, []string{"billing", "bug", "account"})
			if *label.Choice != "bug" {
				t.Errorf("label = %q, want bug", *label.Choice)
			}
			for option, p := range label.Probabilities {
				if p > label.Probabilities[*label.Choice] {
					t.Errorf("chosen option has lower probability than %q", option)
				}
			}
			urgency := result.Answers["urgency"]
			if urgency.Type != "score" || urgency.Score == nil || urgency.Confidence == nil || len(urgency.Legend) != len(levels) {
				t.Fatalf("invalid score answer: %+v", urgency)
			}
			requireProbability(t, *urgency.Confidence)
			requireDistribution(t, urgency.Probabilities, []string{"0", "1", "2"})
			var weighted float64
			for i, level := range levels {
				key := strconv.Itoa(i)
				if urgency.Legend[key] != level {
					t.Errorf("legend[%q] = %q, want %q", key, urgency.Legend[key], level)
				}
				weighted += float64(i) * urgency.Probabilities[key]
			}
			if math.Abs(*urgency.Score-weighted) > 1e-5 || *urgency.Score < 0 || *urgency.Score > 2 {
				t.Errorf("score %v does not match weighted rubric %v", *urgency.Score, weighted)
			}
			t.Logf("%s: %s", model, resp.RawBody)
		})
	}
}

func requireDistribution(t *testing.T, values map[string]float64, keys []string) {
	t.Helper()
	if len(values) != len(keys) {
		t.Fatalf("probability keys = %v, want %v", values, keys)
	}
	var total float64
	for _, key := range keys {
		p, ok := values[key]
		if !ok {
			t.Fatalf("missing probability for %q", key)
		}
		requireProbability(t, p)
		total += p
	}
	if math.Abs(total-1) > 1e-5 {
		t.Errorf("probabilities sum to %v, want 1", total)
	}
}

func requireProbability(t *testing.T, value float64) {
	t.Helper()
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		t.Fatalf("invalid probability: %v", value)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func loadDotenv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, ".env")
		if _, err := os.Stat(path); err == nil {
			_ = godotenv.Load(path)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
