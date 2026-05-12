package proxy

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) Favicon() (string, []byte, bool) {
	if len(s.faviconData) == 0 {
		return "", nil, false
	}

	return s.faviconContentType, s.faviconData, true
}

func (s *Server) initFavicon() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	hc := &http.Client{Transport: s.rt}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "wingman",
		Version: "1.0.0"},
		nil,
	)

	session, err := client.Connect(
		ctx,
		&mcpsdk.StreamableClientTransport{
			Endpoint:   s.url.String(),
			HTTPClient: hc,
		},
		nil,
	)

	if err != nil {
		return
	}

	defer session.Close()

	result := session.InitializeResult()
	if result == nil || result.ServerInfo == nil {
		return
	}

	for _, icon := range result.ServerInfo.Icons {
		if contentType, data, ok := resolveIcon(hc, icon); ok {
			s.faviconContentType = contentType
			s.faviconData = data
			return
		}
	}
}

func resolveIcon(hc *http.Client, icon mcpsdk.Icon) (string, []byte, bool) {
	// data URI: data:[<mediatype>][;base64],<data>
	if rest, ok := strings.CutPrefix(icon.Source, "data:"); ok {
		meta, encoded, ok := strings.Cut(rest, ",")
		if !ok {
			return "", nil, false
		}

		mimeType, isBase64 := strings.CutSuffix(meta, ";base64")
		if mimeType == "" {
			mimeType = icon.MIMEType
		}

		if isBase64 {
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return "", nil, false
			}
			return mimeType, data, true
		}

		return mimeType, []byte(encoded), true
	}

	// HTTP/HTTPS URL
	resp, err := hc.Get(icon.Source)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", nil, false
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 {
		return "", nil, false
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = icon.MIMEType
	}

	return contentType, data, true
}
