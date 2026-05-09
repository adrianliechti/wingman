//go:build smoke

package codex_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/codex"
)

// TestSmokeBasic exercises a real `codex app-server` subprocess. Skipped
// unless the codex binary is on PATH and the build tag `smoke` is set:
//
//	go test -tags=smoke -run TestSmokeBasic -v ./pkg/provider/codex
func TestSmokeBasic(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex binary not on PATH")
	}

	c, err := codex.NewCompleter("gpt-5.4-mini")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	msgs := []provider.Message{
		provider.UserMessage("Reply with exactly the word PONG and nothing else."),
	}

	var (
		text  strings.Builder
		final *provider.Completion
	)

	for completion, err := range c.Complete(ctx, msgs, nil) {
		if err != nil {
			t.Fatalf("complete: %v", err)
		}
		if completion == nil {
			continue
		}
		if completion.Status != "" {
			final = completion
			continue
		}
		if completion.Message != nil {
			for _, content := range completion.Message.Content {
				if content.Text != "" {
					text.WriteString(content.Text)
				}
			}
		}
	}

	if final == nil {
		t.Fatal("no terminal completion received")
	}
	got := strings.TrimSpace(text.String())
	t.Logf("status=%s usage=%+v text=%q", final.Status, final.Usage, got)
	if got == "" {
		t.Fatal("no assistant text received")
	}
	if !strings.Contains(strings.ToUpper(got), "PONG") {
		t.Errorf("expected reply to contain PONG, got %q", got)
	}
}
