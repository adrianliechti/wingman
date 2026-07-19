package responses

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
)

// Differential test: the same JSON body is posted to the local
// /responses/input_tokens handler and to the real OpenAI endpoint (free of
// charge), and the estimates must stay within tolerance of the reference.
// Gated:
//
//	TOKENS_LIVE=1 OPENAI_API_KEY=... go test -run TestInputTokensVsReference -v ./server/openai/responses/
func TestInputTokensVsReference(t *testing.T) {
	if os.Getenv("TOKENS_LIVE") != "1" {
		t.Skip("set TOKENS_LIVE=1 to compare against the live OpenAI API")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	tools := []map[string]any{
		{
			"type":        "function",
			"name":        "get_weather",
			"description": "Get the current weather for a location, including temperature, conditions, and wind.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{"type": "string", "description": "City and country, e.g. Zurich, Switzerland"},
					"unit":     map[string]any{"type": "string", "enum": []string{"celsius", "fahrenheit"}},
				},
				"required": []string{"location"},
			},
		},
		{
			"type":        "function",
			"name":        "search_flights",
			"description": "Search available flights between two airports on a given date, sorted by price.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"origin":      map[string]any{"type": "string", "description": "IATA airport code, e.g. ZRH"},
					"destination": map[string]any{"type": "string", "description": "IATA airport code, e.g. LIS"},
					"date":        map[string]any{"type": "string", "format": "date"},
					"passengers":  map[string]any{"type": "integer", "minimum": 1, "maximum": 9},
					"cabin":       map[string]any{"type": "string", "enum": []string{"economy", "premium", "business"}},
				},
				"required": []string{"origin", "destination", "date"},
			},
		},
		{
			"type":        "function",
			"name":        "run_sql",
			"description": "Execute a read-only SQL query against the analytics warehouse and return rows as JSON.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":   map[string]any{"type": "string"},
					"timeout": map[string]any{"type": "integer", "description": "Query timeout in seconds"},
				},
				"required": []string{"query"},
			},
		},
	}

	scenarios := map[string]map[string]any{
		"multi_tool_conversation": {
			"model":        "gpt-5",
			"instructions": "You are a travel planning assistant. Prefer the cheapest reasonable option and always confirm before booking.",
			"tools":        tools,
			"input": []map[string]any{
				{"role": "user", "content": "I need to fly from Zurich to Lisbon next Friday, and I'd like to know the weather there."},
				{"type": "function_call", "call_id": "call_01", "name": "search_flights",
					"arguments": `{"origin":"ZRH","destination":"LIS","date":"2026-07-24","passengers":1,"cabin":"economy"}`},
				{"type": "function_call_output", "call_id": "call_01",
					"output": `[{"flight":"TP921","depart":"07:35","arrive":"09:20","price_eur":142.90},{"flight":"LX2084","depart":"12:10","arrive":"13:55","price_eur":189.00}]`},
			},
		},
		"image_data_and_tools": {
			"model":        "gpt-5",
			"instructions": "You analyze charts and verify the numbers against the warehouse.",
			"tools":        tools,
			"input": []map[string]any{
				{"role": "user", "content": []map[string]any{
					{"type": "input_text", "text": "Here is the Q2 revenue chart:"},
					{"type": "input_image", "image_url": "data:image/png;base64," + testPNGResponses(t, 1200, 800)},
					{"type": "input_text", "text": "And the raw numbers:\n\nregion,month,revenue_chf\neu-west,2026-04,412903.55\neu-west,2026-05,438221.10\neu-west,2026-06,455872.94\nus-east,2026-04,689455.21\nus-east,2026-05,702134.88\nus-east,2026-06,731092.47\n\nDoes the chart match? Verify against the warehouse with run_sql."},
				}},
			},
		},
	}

	cfg := &config.Config{Policy: noop.New()}
	h := New(cfg)

	for name, body := range scenarios {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}

		reference := openAIReference(t, payload)
		local := localInputTokens(t, h, payload)

		pct := 100 * math.Abs(float64(local)-float64(reference)) / float64(reference)
		t.Logf("%-24s reference=%-6d wingman=%-6d err=%.1f%%", name, reference, local, pct)
		if pct > 20 {
			t.Errorf("%s: wingman %d vs reference %d (%.1f%% > 20%%)", name, local, reference, pct)
		}
	}
}

func localInputTokens(t *testing.T, h *Handler, payload []byte) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/responses/input_tokens", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleInputTokens(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("local handler: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp InputTokensResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.InputTokens
}

func openAIReference(t *testing.T, payload []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses/input_tokens", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENAI_API_KEY"))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out struct {
		InputTokens int `json:"input_tokens"`
		Error       *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("openai reference (HTTP %d): %s", resp.StatusCode, out.Error.Message)
	}
	return out.InputTokens
}

// testPNGResponses renders the same deterministic chart stand-in as the
// anthropic-side differential test.
func testPNGResponses(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 180, A: 255}
			if x%97 == 0 || y%71 == 0 {
				c = color.RGBA{R: 40, G: 40, B: 40, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}
