package tika

import (
	"net/http"
)

var SupportedExtensions = []string{
	".pdf",

	".jpg", ".jpeg",
	".png",

	".doc", ".docx",
	".ppt", ".pptx",
	".xls", ".xlsx",
}

var SupportedMimeTypes = []string{
	"application/pdf",

	"image/jpeg",
	"image/png",

	"application/msword",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",

	"application/vnd.ms-powerpoint",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation",

	"application/vnd.ms-excel",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

type Option func(*Client)

func WithClient(client *http.Client) Option {
	return func(c *Client) {
		c.client = client
	}
}
