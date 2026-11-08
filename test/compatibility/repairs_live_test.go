package compatibility_test

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Complement the chat app's live OpenAI SDK comparison with the reverse path:
// a native Anthropic SDK consuming OpenAI-backed Messages and replaying its
// opaque reasoning. The caller supplies an explicit running gateway URL.
func TestRepairLiveAnthropicSDKReplay(t *testing.T) {
	endpoint := os.Getenv("WINGMAN_SDK_LIVE_URL")
	if endpoint == "" {
		t.Skip("set WINGMAN_SDK_LIVE_URL to run paid live SDK replay checks")
	}
	models := os.Getenv("WINGMAN_SDK_MODELS")
	if models == "" {
		models = "gpt-6-luna,claude-sonnet-5-5"
	}
	client := anthropic.NewClient(option.WithBaseURL(strings.TrimSuffix(endpoint, "/v1")), option.WithAPIKey("test"), option.WithMaxRetries(0), option.WithRequestTimeout(90*time.Second))
	for _, model := range strings.Split(models, ",") {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				input := []any{map[string]any{"role": "user", "content": "Think carefully and verify independently. Find the smallest positive integer x with x mod 7 = 3, x mod 11 = 5, and x mod 13 = 7. Remember the code ORCHID-x using the numeric answer in place of x. Reply only with that code."}}
				for turn := 0; turn < 2; turn++ {
					params := anthropic.MessageNewParams{Model: anthropic.Model(model), MaxTokens: 4096}
					opts := []option.RequestOption{option.WithJSONSet("messages", input), option.WithJSONSet("thinking", map[string]any{"type": "adaptive"}), option.WithJSONSet("output_config", map[string]any{"effort": "high"})}
					var message *anthropic.Message
					if stream {
						events := client.Messages.NewStreaming(t.Context(), params, opts...)
						message = &anthropic.Message{}
						for events.Next() {
							if err := message.Accumulate(events.Current()); err != nil {
								events.Close()
								t.Fatal(err)
							}
						}
						err := events.Err()
						events.Close()
						if err != nil {
							t.Fatal(err)
						}
					} else {
						var err error
						message, err = client.Messages.New(t.Context(), params, opts...)
						if err != nil {
							t.Fatal(err)
						}
					}
					var answer strings.Builder
					signed := false
					for _, part := range message.Content {
						if part.Type == "text" {
							answer.WriteString(part.Text)
						}
						if part.Type == "thinking" && part.Signature != "" || part.Type == "redacted_thinking" && part.Data != "" {
							signed = true
						}
					}
					if strings.TrimSpace(answer.String()) != "ORCHID-423" || message.StopReason != "end_turn" {
						t.Fatalf("turn %d answer=%q stop=%s", turn, answer.String(), message.StopReason)
					}
					if message.Usage.InputTokens <= 0 || message.Usage.OutputTokens <= 0 {
						t.Fatalf("usage lost: %+v", message.Usage)
					}
					if turn == 0 && !signed {
						t.Fatal("reasoning signature missing; replay would not exercise the repair boundary")
					}
					var wire map[string]any
					if err := json.Unmarshal([]byte(message.RawJSON()), &wire); err != nil {
						t.Fatal(err)
					}
					input = append(input, map[string]any{"role": "assistant", "content": wire["content"]}, map[string]any{"role": "user", "content": "Repeat the code, with no other text."})
				}
			})
		}
	}
}
