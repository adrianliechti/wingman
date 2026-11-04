package bedrock

import (
	"bytes"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Converse accepts Office documents, but Claude rejects a cachePoint directly
// after a non-PDF document. Preserve the document and cache the preceding text.
func TestDocumentCachePointPlacement(t *testing.T) {
	c := &Completer{Config: &Config{model: "eu.anthropic.claude-sonnet-4-6"}}
	for mime, format := range documentFormats {
		t.Run(mime, func(t *testing.T) {
			data := []byte{0, 255, 42, 128}
			messages := []provider.Message{{Role: provider.MessageRoleUser, Content: []provider.Content{
				provider.TextContent("Read the attached document."),
				provider.FileContent(&provider.File{ContentType: mime, Content: data}),
			}}}
			req, err := c.convertConverseInput(messages, &provider.CompleteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"text", "cache", "document"}
			if format == types.DocumentFormatPdf {
				want = []string{"text", "document", "cache"}
			}
			if got := documentBlockKinds(req.Messages[0].Content); !slices.Equal(got, want) {
				t.Fatalf("blocks = %v, want %v", got, want)
			}
			for _, block := range req.Messages[0].Content {
				if document, ok := block.(*types.ContentBlockMemberDocument); ok {
					if document.Value.Format != format {
						t.Fatalf("format = %q, want %q", document.Value.Format, format)
					}
					if aws.ToString(document.Value.Name) == "" {
						t.Fatal("document must have a neutral name")
					}
					source, ok := document.Value.Source.(*types.DocumentSourceMemberBytes)
					if !ok || !bytes.Equal(source.Value, data) {
						t.Fatal("document bytes changed")
					}
				}
			}
			if len(messages[0].Content) != 2 || !bytes.Equal(messages[0].Content[1].File.Content, data) {
				t.Fatal("original conversation was mutated")
			}
		})
	}
}

func TestDocumentCachePointPlacementMixedContent(t *testing.T) {
	word := provider.FileContent(&provider.File{
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Content:     []byte("word bytes"),
	})
	pdf := provider.FileContent(&provider.File{ContentType: "application/pdf", Content: []byte("pdf bytes")})
	text := provider.TextContent("Read the attached documents.")
	cases := []struct {
		name    string
		model   string
		content []provider.Content
		options *provider.CompleteOptions
		want    []string
	}{
		{"two Word documents", "anthropic.claude-sonnet-4-6", []provider.Content{text, word, word}, nil, []string{"text", "cache", "document", "document"}},
		{"trailing text", "anthropic.claude-sonnet-4-6", []provider.Content{word, text}, nil, []string{"document", "text", "cache"}},
		{"trailing PDF", "anthropic.claude-sonnet-4-6", []provider.Content{text, word, pdf}, nil, []string{"text", "document", "document", "cache"}},
		{"PDF before Word", "anthropic.claude-sonnet-4-6", []provider.Content{text, pdf, word}, nil, []string{"text", "document", "cache", "document"}},
		{"no cacheable prefix", "anthropic.claude-sonnet-4-6", []provider.Content{word, word}, nil, []string{"document", "document"}},
		{"explicit caching", "anthropic.claude-sonnet-4-6", []provider.Content{{Text: text.Text, CacheControl: &provider.CacheControl{}}, word}, &provider.CompleteOptions{CacheOptions: &provider.CacheOptions{Mode: provider.CacheModeExplicit}}, []string{"text", "cache", "document"}},
		{"Nova", "amazon.nova-pro-v1:0", []provider.Content{text, word}, nil, []string{"text", "document"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Completer{Config: &Config{model: tc.model}}
			req, err := c.convertConverseInput([]provider.Message{{Role: provider.MessageRoleUser, Content: tc.content}}, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if got := documentBlockKinds(req.Messages[0].Content); !slices.Equal(got, tc.want) {
				t.Fatalf("blocks = %v, want %v", got, tc.want)
			}
		})
	}
}

func documentBlockKinds(content []types.ContentBlock) []string {
	var kinds []string
	for _, block := range content {
		switch block.(type) {
		case *types.ContentBlockMemberText:
			kinds = append(kinds, "text")
		case *types.ContentBlockMemberDocument:
			kinds = append(kinds, "document")
		case *types.ContentBlockMemberCachePoint:
			kinds = append(kinds, "cache")
		default:
			kinds = append(kinds, "other")
		}
	}
	return kinds
}
