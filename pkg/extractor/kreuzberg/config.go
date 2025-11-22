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
