package mcp

import "net/http"

type Provider interface {
	http.Handler

	Icon() (contentType string, data []byte)
}
