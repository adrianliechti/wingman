package config

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

func TestDefaultExtractorRecursivelyExtractsAttachments(t *testing.T) {
	input := strings.Join([]string{
		"From: sender@example.com",
		"To: recipient@example.com",
		"Subject: Default Email",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=\"message\"",
		"",
		"--message",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Email body",
		"--message",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Disposition: attachment; filename=\"notes.txt\"",
		"",
		"Recursive attachment body",
		"--message--",
		"",
	}, "\r\n")

	provider, err := defaultExtractor()
	if err != nil {
		t.Fatal(err)
	}

	document, err := provider.Extract(context.Background(), extractor.File{
		Name:    "message.eml",
		Content: []byte(input),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"# Default Email",
		"Email body",
		"## Attachment contents",
		"### notes.txt",
		"Recursive attachment body",
	} {
		if !strings.Contains(document.Text, expected) {
			t.Errorf("document does not contain %q:\n%s", expected, document.Text)
		}
	}
}
