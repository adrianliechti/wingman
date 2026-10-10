package mistral

import (
	"net/http"
	"slices"
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

func WithModel(model string) Option {
	return func(c *Client) { c.model = model }
}

// Keep the request media types and advertised formats in one place.
var supportedFormats = map[string]string{
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".avif": "image/avif",
}

var SupportedExtensions, SupportedMimeTypes = supportedTypes()

func supportedTypes() ([]string, []string) {
	var extensions, mediaTypes []string
	for extension, mediaType := range supportedFormats {
		extensions = append(extensions, extension)
		mediaTypes = append(mediaTypes, mediaType)
	}
	slices.Sort(extensions)
	slices.Sort(mediaTypes)
	return extensions, slices.Compact(mediaTypes)
}
