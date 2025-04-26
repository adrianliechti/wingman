package azure

import (
	"net/http"
)

type Option func(*Client)

func WithClient(client *http.Client) Option {
	return func(c *Client) {
		c.client = client
	}
}

func WithToken(token string) Option {
	return func(c *Client) {
		c.token = token
	}
}

// https://learn.microsoft.com/en-us/azure/ai-services/document-intelligence/concept-layout?view=doc-intel-4.0.0&tabs=sample-code#input-requirements
var SupportedExtensions = []string{
	".pdf",

	".jpeg", ".jpg",
	".png",
	".bmp",
	".tiff",
	".heif",

	".docx",
	".pptx",
	".xlsx",
}

// https://learn.microsoft.com/en-us/azure/ai-services/document-intelligence/concept-layout?view=doc-intel-4.0.0&tabs=sample-code#input-requirements
var SupportedMimeTypes = []string{
	"application/pdf",

	"image/jpeg",
	"image/png",
	"image/bmp",
	"image/tiff",
	"image/heif",

	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}
