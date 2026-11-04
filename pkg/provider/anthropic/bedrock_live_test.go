package anthropic

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/joho/godotenv"
)

// ANTHROPIC_BEDROCK_LIVE=1 go test -v ./pkg/provider/anthropic -run TestBedrockLive -count=1
func TestBedrockLive(t *testing.T) {
	if os.Getenv("ANTHROPIC_BEDROCK_LIVE") != "1" {
		t.Skip("set ANTHROPIC_BEDROCK_LIVE=1 to call Bedrock Runtime and Mantle")
	}
	values, _ := godotenv.Read("../../../.env")
	value := func(name, fallback string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		if v := values[name]; v != "" {
			return v
		}
		return fallback
	}
	key, region := value("AWS_BEARER_TOKEN_BEDROCK", ""), value("AWS_REGION", "")
	if key == "" || region == "" {
		t.Fatal("AWS_BEARER_TOKEN_BEDROCK and AWS_REGION are required")
	}
	for _, target := range []struct{ name, url, model string }{
		{"runtime", "https://bedrock-runtime." + region + ".amazonaws.com", value("ANTHROPIC_BEDROCK_MODEL", "eu.anthropic.claude-sonnet-5-5")},
		{"mantle", "https://bedrock-mantle." + region + ".api.aws/anthropic", value("ANTHROPIC_MANTLE_MODEL", "anthropic.claude-sonnet-5")},
	} {
		t.Run(target.name, func(t *testing.T) {
			// Use the explicit provider token, even if a different AWS token is in the environment.
			t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "unused-environment-token")
			client, err := NewCompleter(target.url, target.model, WithToken(key), WithClient(&http.Client{Timeout: 60 * time.Second}), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			var result provider.CompletionAccumulator
			for chunk, err := range client.Complete(ctx, []provider.Message{provider.UserMessage("Reply with exactly OK.")}, &provider.CompleteOptions{
				MaxTokens: new(512), ReasoningOptions: &provider.ReasoningOptions{Effort: provider.EffortLow},
			}) {
				if err != nil {
					t.Fatal(strings.ReplaceAll(err.Error(), key, "[redacted]"))
				}
				result.Add(*chunk)
			}
			completion := result.Result()
			if strings.TrimSpace(completion.Text()) != "OK" || completion.StopReason != provider.StopReasonEndTurn || completion.Usage == nil || completion.Usage.OutputTokens == 0 {
				t.Fatalf("incomplete response: %+v", completion)
			}
			t.Logf("%s: model=%s text=%q input=%d output=%d", region, target.model, completion.Text(), completion.Usage.InputTokens, completion.Usage.OutputTokens)
		})
	}
}
