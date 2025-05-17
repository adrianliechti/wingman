package unstructured

import (
	"net/http"
)

// https://docs.unstructured.io/api-reference/api-services/supported-file-types
var SupportedExtensions = []string{
	".bmp",
	".csv",
	".doc",
	".docx",
	".eml",
	".epub",
	".heic",
	".html",
	".jpeg",
	".png",
	".md",
	".msg",
	".odt",
	".org",
	".p7s",
	".pdf",
	".png",
	".ppt",
	".pptx",
	".rst",
	".rtf",
	".tiff",
	".txt",
	".tsv",
	".xls",
	".xlsx",
	".xml",
}

var SupportedMimeTypes = []string{
	"image/bmp",
	"text/csv",
	"application/msword",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"message/rfc822",
	"application/epub+zip",
	"image/heic",
	"text/html",
	"image/jpeg",
	"image/png",
	"text/markdown",
	"application/vnd.ms-outlook",
	"application/vnd.oasis.opendocument.text",
	"text/org",
	"application/pkcs7-signature",
	"application/pdf",
	"image/png",
	"application/vnd.ms-powerpoint",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"text/rst",
	"application/rtf",
	"image/tiff",
	"text/plain",
	"text/tab-separated-values",
	"application/vnd.ms-excel",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"application/xml",
}

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

func WithStrategy(strategy Strategy) Option {
	return func(c *Client) {
		c.strategy = strategy
	}
}
