package gemini

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/google"

	"github.com/joho/godotenv"
	"google.golang.org/genai"
)

// TestFunctionCallSignatureReplayLive runs a Gemini-format tool loop through
// Wingman's Gemini backend with and without thought summaries: the signature returned
// on the function call must be accepted when the client replays the turn.
//
//	WINGMAN_GOOGLE_LIVE=1 go test -v ./server/gemini -run TestFunctionCallSignatureReplayLive -count=1
func TestFunctionCallSignatureReplayLive(t *testing.T) {
	if os.Getenv("WINGMAN_GOOGLE_LIVE") != "1" {
		t.Skip("set WINGMAN_GOOGLE_LIVE=1 to call the Gemini API")
	}
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		values, _ := godotenv.Read("../../.env")
		key = values["GEMINI_API_KEY"]
	}
	if key == "" {
		t.Fatal("GEMINI_API_KEY is required")
	}
	models := os.Getenv("WINGMAN_GOOGLE_LIVE_MODELS")
	if models == "" {
		models = "gemini-3.8-flash"
	}
	for model := range strings.SplitSeq(models, ",") {
		for _, tc := range []struct{ stream, summaries, native bool }{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, false}, {false, false, true}} {
			t.Run(fmt.Sprintf("%s/stream=%t/summaries=%t/native=%t", model, tc.stream, tc.summaries, tc.native), func(t *testing.T) {
				p, err := google.NewCompleter(strings.TrimSpace(model), google.WithToken(key))
				if err != nil {
					t.Fatal(err)
				}
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("target", signatures.ScopedTo("target", p))
				h := New(cfg)
				generate := func(body map[string]any) (int, []byte) {
					data, _ := json.Marshal(body)
					req := httptest.NewRequest(http.MethodPost, "/models/target:generateContent", strings.NewReader(string(data)))
					req.SetPathValue("model", "target")
					rec := httptest.NewRecorder()
					if tc.stream {
						req.URL.RawQuery = "alt=sse"
						h.handleStreamGenerateContent(rec, req)
					} else {
						h.handleGenerateContent(rec, req)
					}
					return rec.Code, rec.Body.Bytes()
				}
				readContent := func(body []byte) Content {
					t.Helper()
					reply := Content{Role: "model"}
					chunks := [][]byte{body}
					if tc.stream {
						chunks = nil
						for line := range strings.SplitSeq(string(body), "\n") {
							if after, ok := strings.CutPrefix(line, "data: "); ok {
								chunks = append(chunks, []byte(after))
							}
						}
					}
					for _, chunk := range chunks {
						var turn GenerateContentResponse
						if err := json.Unmarshal(chunk, &turn); err != nil || turn.Error != nil {
							t.Fatalf("response: %v %s", err, chunk)
						}
						if len(turn.Candidates) > 0 && turn.Candidates[0].Content != nil {
							reply.Parts = append(reply.Parts, turn.Candidates[0].Content.Parts...)
						}
					}
					if len(reply.Parts) == 0 {
						t.Fatalf("no response content: %s", body)
					}
					return reply
				}

				question := map[string]any{"role": "user", "parts": []any{map[string]any{"text": "What is the weather in Zurich? Use the tool."}}}
				tools := []any{map[string]any{"functionDeclarations": []any{map[string]any{
					"name": "get_weather", "description": "Current weather for a city",
					"parameters": map[string]any{"type": "OBJECT", "properties": map[string]any{"city": map[string]any{"type": "STRING"}}, "required": []string{"city"}},
				}}}}
				thinking := map[string]any{"thinkingConfig": map[string]any{"thinkingLevel": "low", "includeThoughts": tc.summaries}}

				var code int
				var body []byte
				if tc.native {
					client, err := genai.NewClient(t.Context(), &genai.ClientConfig{APIKey: key, Backend: genai.BackendGeminiAPI})
					if err != nil {
						t.Fatal(err)
					}
					response, err := client.Models.GenerateContent(t.Context(), strings.TrimSpace(model), genai.Text("What is the weather in Zurich? Use the tool."), &genai.GenerateContentConfig{
						Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
							Name: "get_weather", Description: "Current weather for a city",
							ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
						}}}},
						ToolConfig: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny}},
					})
					if err != nil {
						t.Fatal(err)
					}
					body, err = json.Marshal(response)
					if err != nil {
						t.Fatal(err)
					}
					code = 200
				} else {
					code, body = generate(map[string]any{"contents": []any{question}, "tools": tools, "generationConfig": thinking,
						"toolConfig": map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY"}}})
				}
				if code != 200 {
					t.Fatalf("turn 1: %d %s", code, body)
				}
				reply := readContent(body)
				var call *FunctionCall
				signed := false
				for _, part := range reply.Parts {
					if part.FunctionCall != nil && call == nil {
						call = part.FunctionCall
						signed = len(part.ThoughtSignature) > 0
					}
				}
				if call == nil {
					t.Fatalf("no function call: %s", body)
				}
				if !signed {
					t.Fatalf("function call has no thoughtSignature: %s", body)
				}
				t.Logf("function call %s signed=%v", call.Name, signed)

				code, body = generate(map[string]any{"contents": []any{question, reply, map[string]any{"role": "user", "parts": []any{map[string]any{
					"functionResponse": map[string]any{"id": call.ID, "name": call.Name, "response": map[string]any{"temperature": "18C", "sky": "clear"}},
				}}}}, "tools": tools, "generationConfig": thinking})
				if code != 200 {
					t.Fatalf("turn 2 replay rejected: %d %s", code, body)
				}
				var answer strings.Builder
				for _, part := range readContent(body).Parts {
					if !part.Thought {
						answer.WriteString(part.Text)
					}
				}
				if !strings.Contains(answer.String(), "18") {
					t.Fatalf("turn 2 did not use the tool result: %s", body)
				}
			})
		}
	}
}
