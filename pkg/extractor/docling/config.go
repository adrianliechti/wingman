package docling

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
