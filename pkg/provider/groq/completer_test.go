package groq_test

import (
	"context"
	"os"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/groq"

	"github.com/stretchr/testify/require"
)

func TestCompleter(t *testing.T) {
	ctx := context.Background()
	token := os.Getenv("GROQ_API_TOKEN")
	model := "llama-3.1-8b-instant"

	if token == "" {
		t.Skip("GROQ_API_TOKEN required for this test")
	}

	c, err := groq.NewCompleter(model, groq.WithToken(token))
	require.NoError(t, err)

	result, err := c.Complete(ctx, []provider.Message{
		provider.UserMessage("Hello!"),
	}, nil)

	require.NoError(t, err)
	require.NotEmpty(t, result.Message.Content)

	t.Log(result.Message.Content)
}
