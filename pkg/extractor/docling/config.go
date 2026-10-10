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
	".tif", ".tiff",
	".webp",

	".docx",
	".pptx",
	".xlsx",
	".html", ".htm", ".xhtml",
	".md", ".markdown",
	".adoc", ".asciidoc",
	".csv",
	".vtt",
	".xml",
}

var SupportedMimeTypes = []string{
	"application/pdf",

	"image/jpeg",
	"image/png",
	"image/bmp",
	"image/tiff",
	"image/webp",

	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"text/html",
	"application/xhtml+xml",
	"text/markdown",
	"text/asciidoc",
	"text/csv",
	"text/vtt",
	"application/xml", "text/xml",
}
