package mcp

import "net/http"

type Provider interface {
	http.Handler
}
