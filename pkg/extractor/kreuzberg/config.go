package kreuzberg

import (
	"net/http"
)

var SupportedExtensions = []string{
	".pdf",

	".doc",
	".docx",
	".xls",
	".xlsx",
	".ppt",
	".pptx",

	".odt",
	".ods",

	".epub",

	".png",
	".jpg",
	".jpeg",
	".gif",
	".webp",
	".bmp",
	".tiff",
	".tif",

	".html",
	".htm",
	".xml",
	".json",
	".csv",
	".txt",
	".md",

	".eml",
	".msg",
}

var SupportedMimeTypes = []string{
	"application/pdf",

	"application/msword",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"application/vnd.ms-excel",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"application/vnd.ms-powerpoint",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation",

	"application/vnd.oasis.opendocument.text",
	"application/vnd.oasis.opendocument.spreadsheet",

	"application/epub+zip",

	"image/png",
	"image/jpeg",
	"image/gif",
	"image/webp",
	"image/bmp",
	"image/tiff",

	"text/html",
	"application/xhtml+xml",
	"application/xml",
	"text/xml",
	"application/json",
	"text/csv",
	"text/plain",
	"text/markdown",

	"message/rfc822",
	"application/vnd.ms-outlook",
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
