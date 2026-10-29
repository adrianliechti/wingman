package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/joho/godotenv"
	"google.golang.org/genai"
)

// Opt in to paid, network-dependent comparison tests with:
// WINGMAN_GOOGLE_LIVE=1 go test -v ./pkg/provider/google -run TestComplete_LiveComparison -count=1
// WINGMAN_GOOGLE_LIVE_MODELS accepts a comma-separated model list.
func TestComplete_LiveComparison(t *testing.T) {
	if os.Getenv("WINGMAN_GOOGLE_LIVE") != "1" {
		t.Skip("set WINGMAN_GOOGLE_LIVE=1 to compare the real Gemini APIs")
	}
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		values, _ := godotenv.Read("../../../.env")
		key = values["GEMINI_API_KEY"]
	}
	if key == "" {
		t.Fatal("GEMINI_API_KEY is required")
	}
	models := os.Getenv("WINGMAN_GOOGLE_LIVE_MODELS")
	if models == "" {
		models = "gemini-3.8-flash"
	}
	client := &http.Client{Timeout: 90 * time.Second}
	if directory := os.Getenv("WINGMAN_GOOGLE_LIVE_CAPTURE"); directory != "" {
		client.Transport = &liveCaptureTransport{directory: directory}
	}
	for _, model := range strings.Split(models, ",") {
		model = strings.TrimSpace(model)
		t.Run(model, func(t *testing.T) {
			completer, err := NewCompleter(model, WithToken(key), WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := genai.NewClient(t.Context(), &genai.ClientConfig{
				APIKey: key, Backend: genai.BackendGeminiAPI, HTTPClient: client,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, effort := range []provider.Effort{"", provider.EffortLow, provider.EffortMedium, provider.EffortHigh} {
				name := string(effort)
				if name == "" {
					name = "default"
				}
				t.Run("thinking_"+name, func(t *testing.T) {
					prompt := "What is 17 * 19? Reply with only the integer."
					options := &provider.CompleteOptions{MaxTokens: new(2048), ReasoningOptions: &provider.ReasoningOptions{Effort: effort, IncludeSummary: true}}
					result := liveComplete(t, completer, []provider.Message{provider.UserMessage(prompt)}, options)
					old := liveGenerate(t, baseline, model, genai.Text(prompt), liveGenerationConfig(model, effort, true))
					liveWantText(t, result.Message.Text(), "323")
					liveWantText(t, old.text, "323")
					liveWantCompleted(t, result, old)
				})
			}
			t.Run("system_and_history", func(t *testing.T) {
				instruction := "When asked for a code, reply with only the code from the conversation."
				messages := []provider.Message{
					provider.SystemMessage(instruction), provider.UserMessage("My code is TULIP42."),
					provider.AssistantMessage("Understood."), provider.UserMessage("What is my code?"),
				}
				result := liveComplete(t, completer, messages, &provider.CompleteOptions{MaxTokens: new(2048)})
				config := liveGenerationConfig(model, "", false)
				config.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: instruction}}}
				old := liveGenerate(t, baseline, model, []*genai.Content{
					{Role: "user", Parts: []*genai.Part{{Text: "My code is TULIP42."}}},
					{Role: "model", Parts: []*genai.Part{{Text: "Understood."}}},
					{Role: "user", Parts: []*genai.Part{{Text: "What is my code?"}}},
				}, config)
				liveWantText(t, result.Message.Text(), "TULIP42")
				liveWantText(t, old.text, "TULIP42")
				liveWantCompleted(t, result, old)
			})
			t.Run("structured_json", func(t *testing.T) {
				prompt := "Return the answer 42."
				schema := map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "integer"}}, "required": []string{"answer"}, "additionalProperties": false}
				result := liveComplete(t, completer, []provider.Message{provider.UserMessage(prompt)}, &provider.CompleteOptions{
					MaxTokens: new(2048), Schema: &provider.Schema{Properties: schema},
				})
				config := liveGenerationConfig(model, "", false)
				config.ResponseMIMEType, config.ResponseJsonSchema = "application/json", schema
				old := liveGenerate(t, baseline, model, genai.Text(prompt), config)
				for _, text := range []string{result.Message.Text(), old.text} {
					var output map[string]int
					if err := json.Unmarshal([]byte(text), &output); err != nil || len(output) != 1 || output["answer"] != 42 {
						t.Errorf("invalid structured result: %q (%v)", text, err)
					}
				}
				liveWantCompleted(t, result, old)
			})
			t.Run("max_tokens", func(t *testing.T) {
				prompt := "List all integers from 1 through 1000, one per line. Do not abbreviate."
				result := liveComplete(t, completer, []provider.Message{provider.UserMessage(prompt)}, &provider.CompleteOptions{MaxTokens: new(32)})
				config := liveGenerationConfig(model, "", false)
				config.MaxOutputTokens = 32
				old := liveGenerate(t, baseline, model, genai.Text(prompt), config)
				if result.Status != provider.CompletionStatusIncomplete || result.StopReason != provider.StopReasonMaxTokens || old.finish != genai.FinishReasonMaxTokens {
					t.Errorf("token limit mismatch: Interactions %s/%s, generateContent %s", result.Status, result.StopReason, old.finish)
				}
			})
			t.Run("image_input", func(t *testing.T) {
				picture := image.NewRGBA(image.Rect(0, 0, 64, 64))
				for y := range 64 {
					for x := range 64 {
						picture.Set(x, y, color.RGBA{R: 255, A: 255})
					}
				}
				var buffer bytes.Buffer
				if err := png.Encode(&buffer, picture); err != nil {
					t.Fatal(err)
				}
				prompt := "Name the solid color in this image. Reply with only the uppercase color name."
				messages := []provider.Message{{Role: provider.MessageRoleUser, Content: []provider.Content{
					provider.TextContent(prompt), provider.FileContent(&provider.File{ContentType: "image/png", Content: buffer.Bytes()}),
				}}}
				result := liveComplete(t, completer, messages, &provider.CompleteOptions{MaxTokens: new(2048)})
				old := liveGenerate(t, baseline, model, []*genai.Content{{Role: "user", Parts: []*genai.Part{
					{Text: prompt}, {InlineData: &genai.Blob{MIMEType: "image/png", Data: buffer.Bytes()}},
				}}}, liveGenerationConfig(model, "", false))
				liveWantText(t, result.Message.Text(), "RED")
				liveWantText(t, old.text, "RED")
				liveWantCompleted(t, result, old)
			})
			t.Run("custom_tool_and_replay", func(t *testing.T) {
				input := "SELECT 'Zürich 🌧️';"
				prompt := "Call sql with exactly this input: " + input + " After the result, reply with only DONE."
				options := &provider.CompleteOptions{
					MaxTokens: new(2048), Tools: []provider.Tool{{Kind: provider.ToolKindCustom, Name: "sql", Description: "Execute a SQL statement."}},
					ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceAny}, ReasoningOptions: &provider.ReasoningOptions{Effort: provider.EffortLow},
				}
				messages := []provider.Message{provider.UserMessage(prompt)}
				result := liveComplete(t, completer, messages, options)
				calls := result.Message.ToolCalls()
				if len(calls) != 1 || calls[0].Name != "sql" || calls[0].Kind != provider.ToolKindCustom || calls[0].Arguments != input {
					t.Fatalf("custom input did not survive streaming: %+v", calls)
				}
				messages = append(messages, *result.Message, provider.ToolMessage(calls[0].ID, "success"))
				options.ToolOptions = &provider.ToolOptions{Choice: provider.ToolChoiceNone}
				final := liveComplete(t, completer, messages, options)

				config := liveGenerationConfig(model, provider.EffortLow, false)
				config.Tools = []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "sql", Description: options.Tools[0].Description, ParametersJsonSchema: map[string]any{
					"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}, "required": []string{"input"},
				}}}}}
				config.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny}}
				contents := genai.Text(prompt)
				old := liveGenerate(t, baseline, model, contents, config)
				var call *genai.FunctionCall
				for _, part := range old.content.Parts {
					if part.FunctionCall != nil {
						call = part.FunctionCall
					}
				}
				if call == nil || call.Name != "sql" || call.Args["input"] != input {
					t.Fatal("generateContent custom input mismatch")
				}
				contents = append(contents, old.content, &genai.Content{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: call.ID, Name: call.Name, Response: map[string]any{"output": "success"}}}}})
				config.ToolConfig.FunctionCallingConfig.Mode = genai.FunctionCallingConfigModeNone
				oldFinal := liveGenerate(t, baseline, model, contents, config)
				liveWantText(t, final.Message.Text(), "DONE")
				liveWantText(t, oldFinal.text, "DONE")
				liveWantCompleted(t, final, oldFinal)
			})
			t.Run("parallel_tools_and_signed_replay", func(t *testing.T) {
				prompt := "Call get_temperature once for Zurich and once for Paris, both in this turn. Then sum the temperatures and reply with only the integer."
				parameters := map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string", "enum": []string{"Zurich", "Paris"}}}, "required": []string{"city"}}
				tools := []provider.Tool{{Name: "get_temperature", Description: "Get the temperature for a city.", Parameters: parameters}}
				options := &provider.CompleteOptions{MaxTokens: new(2048), Tools: tools, ToolOptions: &provider.ToolOptions{Choice: provider.ToolChoiceAny, Allowed: []string{"get_temperature"}}, ReasoningOptions: &provider.ReasoningOptions{Effort: provider.EffortLow, IncludeSummary: true}}
				messages := []provider.Message{provider.UserMessage(prompt)}
				result := liveComplete(t, completer, messages, options)
				calls := result.Message.ToolCalls()
				if result.Status != provider.CompletionStatusCompleted || result.StopReason != provider.StopReasonToolUse || len(calls) != 2 {
					t.Fatalf("expected two tool calls: %s/%s, %d calls", result.Status, result.StopReason, len(calls))
				}
				messages = append(messages, *result.Message)
				seen := map[string]bool{}
				for _, call := range calls {
					var args struct{ City string }
					if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil || call.Name != "get_temperature" || call.ID == "" || seen[args.City] {
						t.Fatalf("invalid tool call: %+v (%v)", call, err)
					}
					seen[args.City] = true
					messages = append(messages, provider.ToolMessage(call.ID, fmt.Sprintf(`{"temperature":%d}`, liveTemperature(t, args.City))))
				}
				options.ToolOptions = &provider.ToolOptions{Choice: provider.ToolChoiceNone}
				final := liveComplete(t, completer, messages, options)
				liveWantText(t, final.Message.Text(), "19")

				config := liveGenerationConfig(model, provider.EffortLow, true)
				config.Tools = []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "get_temperature", Description: tools[0].Description, ParametersJsonSchema: parameters}}}}
				config.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny, AllowedFunctionNames: []string{"get_temperature"}}}
				contents := genai.Text(prompt)
				old := liveGenerate(t, baseline, model, contents, config)
				contents = append(contents, old.content)
				responses := &genai.Content{Role: "user"}
				for _, part := range old.content.Parts {
					if call := part.FunctionCall; call != nil {
						city, _ := call.Args["city"].(string)
						responses.Parts = append(responses.Parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: call.ID, Name: call.Name, Response: map[string]any{"temperature": liveTemperature(t, city)}}})
					}
				}
				if len(responses.Parts) != 2 {
					t.Fatalf("generateContent returned %d tool calls", len(responses.Parts))
				}
				contents = append(contents, responses)
				config.ToolConfig.FunctionCallingConfig.Mode = genai.FunctionCallingConfigModeNone
				config.ToolConfig.FunctionCallingConfig.AllowedFunctionNames = nil
				oldFinal := liveGenerate(t, baseline, model, contents, config)
				liveWantText(t, oldFinal.text, "19")
				liveWantCompleted(t, final, oldFinal)
			})
		})
	}
}

func liveComplete(t *testing.T, completer *Completer, messages []provider.Message, options *provider.CompleteOptions) *provider.Completion {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	started := time.Now()
	var accumulator provider.CompletionAccumulator
	for delta, err := range completer.Complete(ctx, messages, options) {
		if err != nil {
			t.Fatalf("Interactions: %s", strings.ReplaceAll(err.Error(), completer.token, "[REDACTED]"))
		}
		accumulator.Add(*delta)
	}
	result := accumulator.Result()
	if result.Message == nil || result.Usage == nil || result.Usage.InputTokens <= 0 || result.Usage.OutputTokens < 0 {
		t.Fatal("Interactions omitted message or usage")
	}
	signatures, summaries := 0, 0
	for _, part := range result.Message.Content {
		if r := part.Reasoning; r != nil {
			if r.Signature != "" {
				signatures++
			}
			if r.SummaryText() != "" {
				summaries++
			}
		}
	}
	liveLog(t, map[string]any{"api": "interactions", "elapsed_ms": time.Since(started).Milliseconds(), "status": result.Status, "stop": result.StopReason, "text": result.Message.Text(), "calls": len(result.Message.ToolCalls()), "signatures": signatures, "summaries": summaries, "usage": result.Usage})
	return result
}

type liveGeneration struct {
	text    string
	finish  genai.FinishReason
	content *genai.Content
}

func liveGenerate(t *testing.T, client *genai.Client, model string, contents []*genai.Content, config *genai.GenerateContentConfig) liveGeneration {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	started := time.Now()
	result := liveGeneration{content: &genai.Content{Role: "model"}}
	var usage *genai.GenerateContentResponseUsageMetadata
	for response, err := range client.Models.GenerateContentStream(ctx, model, contents, config) {
		if err != nil {
			t.Fatalf("generateContent: %v", convertError(err))
		}
		if response.UsageMetadata != nil {
			usage = response.UsageMetadata
		}
		for _, candidate := range response.Candidates {
			if candidate.FinishReason != "" {
				result.finish = candidate.FinishReason
			}
			if candidate.Content != nil {
				for _, part := range candidate.Content.Parts {
					// generateContent may stream the signature in a separate
					// empty part. It belongs to the preceding content part.
					if part.Text == "" && part.FunctionCall == nil && part.InlineData == nil {
						if len(part.ThoughtSignature) > 0 && len(result.content.Parts) > 0 {
							result.content.Parts[len(result.content.Parts)-1].ThoughtSignature = part.ThoughtSignature
						}
						continue
					}
					result.content.Parts = append(result.content.Parts, part)
					if !part.Thought {
						result.text += part.Text
					}
				}
			}
		}
	}
	if (usage == nil || usage.PromptTokenCount <= 0) && result.finish != genai.FinishReasonMaxTokens {
		t.Fatal("generateContent omitted usage")
	}
	liveLog(t, map[string]any{"api": "generateContent", "elapsed_ms": time.Since(started).Milliseconds(), "finish": result.finish, "text": result.text, "usage": usage})
	return result
}

// Only the comparison baseline needs the old API's model-dependent budget.
// Production Interactions uses the same levels for both model generations.
func liveGenerationConfig(model string, effort provider.Effort, summaries bool) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{MaxOutputTokens: 2048, ThinkingConfig: &genai.ThinkingConfig{IncludeThoughts: summaries}}
	if strings.HasPrefix(model, "gemini-2") {
		switch effort {
		case provider.EffortLow:
			config.ThinkingConfig.ThinkingBudget = new(int32(1024))
		case provider.EffortMedium:
			config.ThinkingConfig.ThinkingBudget = new(int32(8192))
		case provider.EffortHigh:
			config.ThinkingConfig.ThinkingBudget = new(int32(24576))
		}
	} else {
		config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevel(strings.ToUpper(string(effort)))
	}
	return config
}

func liveTemperature(t *testing.T, city string) int {
	t.Helper()
	switch city {
	case "Zurich":
		return 7
	case "Paris":
		return 12
	default:
		t.Fatalf("unexpected city %q", city)
		return 0
	}
}

func liveWantText(t *testing.T, text, want string) {
	t.Helper()
	if strings.TrimSpace(text) != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func liveWantCompleted(t *testing.T, result *provider.Completion, old liveGeneration) {
	t.Helper()
	if result.Status != provider.CompletionStatusCompleted || result.StopReason != provider.StopReasonEndTurn || old.finish != genai.FinishReasonStop {
		t.Errorf("completion mismatch: Interactions %s/%s, generateContent %s", result.Status, result.StopReason, old.finish)
	}
}

func liveLog(t *testing.T, data map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(encoded))
}

type liveCaptureTransport struct {
	directory string
	sequence  int
}

type liveCaptureBody struct {
	io.ReadCloser
	data []byte
	path string
}

func (t *liveCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.sequence++
	stem := fmt.Sprintf("%s/%02d", t.directory, t.sequence)
	if request.Body != nil {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		request.Body.Close()
		request.Body = io.NopCloser(strings.NewReader(string(data)))
		if err := os.WriteFile(stem+"_request.json", data, 0600); err != nil {
			return nil, err
		}
	}
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &liveCaptureBody{ReadCloser: response.Body, path: stem + "_response.txt"}
	return response, nil
}

func (b *liveCaptureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.data = append(b.data, p[:n]...)
	return n, err
}

func (b *liveCaptureBody) Close() error {
	os.WriteFile(b.path, b.data, 0600)
	return b.ReadCloser.Close()
}
