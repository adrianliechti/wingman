package bedrock

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Explicit mode places cache points only after the client's breakpoints and
// drops the automatic ones; implicit mode keeps the automatic points.
func TestExplicitCacheModePlacesCachePoints(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-sonnet-4-6"}}
	messages := []provider.Message{
		{Role: provider.MessageRoleSystem, Content: []provider.Content{{Text: "rules", CacheControl: &provider.CacheControl{}}}},
		{Role: provider.MessageRoleUser, Content: []provider.Content{{Text: "prefix", CacheControl: &provider.CacheControl{}}, {Text: "question"}}},
	}

	explicit := newCachePolicy(messages, &provider.CompleteOptions{CacheOptions: &provider.CacheOptions{Mode: provider.CacheModeExplicit}})
	system := c.convertSystemPolicy(messages, explicit)
	if len(system) != 2 {
		t.Fatalf("explicit system blocks = %d, want text and its cache point", len(system))
	}
	if _, ok := system[1].(*types.SystemContentBlockMemberCachePoint); !ok {
		t.Errorf("explicit system block 1 = %T, want a cache point", system[1])
	}
	converted, err := c.convertMessagesPolicy(messages, explicit)
	if err != nil {
		t.Fatal(err)
	}
	content := converted[0].Content
	if len(content) != 3 {
		t.Fatalf("explicit user blocks = %d, want text, cache point, text", len(content))
	}
	if _, ok := content[1].(*types.ContentBlockMemberCachePoint); !ok {
		t.Errorf("explicit user block 1 = %T, want a cache point", content[1])
	}
	if _, ok := content[2].(*types.ContentBlockMemberText); !ok {
		t.Errorf("explicit user block 2 = %T, want the unmarked text without a trailing cache point", content[2])
	}

	implicit := c.convertSystemPolicy(messages, cachePolicy{})
	if len(implicit) != 2 {
		t.Fatalf("implicit system blocks = %d, want text and the automatic cache point", len(implicit))
	}
	if _, ok := implicit[1].(*types.SystemContentBlockMemberCachePoint); !ok {
		t.Errorf("implicit system block 1 = %T, want the automatic cache point", implicit[1])
	}
	converted, err = c.convertMessagesPolicy(messages, cachePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	content = converted[0].Content
	if len(content) != 3 {
		t.Fatalf("implicit user blocks = %d, want two texts and the automatic cache point", len(content))
	}
	if _, ok := content[2].(*types.ContentBlockMemberCachePoint); !ok {
		t.Errorf("implicit user block 2 = %T, want the automatic cache point", content[2])
	}
}
