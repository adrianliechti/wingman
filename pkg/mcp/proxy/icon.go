package proxy

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	iconTTL     = 15 * time.Minute
	iconTimeout = 15 * time.Second
)

type iconCache struct {
	contentType string
	data        []byte
	fetchedAt   time.Time
}

func (s *Server) Icon() (string, []byte) {
	if c := s.icon.Load(); c != nil && time.Since(c.fetchedAt) <= iconTTL {
		return c.contentType, c.data
	}

	s.iconMu.Lock()
	defer s.iconMu.Unlock()

	if c := s.icon.Load(); c != nil && time.Since(c.fetchedAt) <= iconTTL {
		return c.contentType, c.data
	}

	contentType, data := s.fetchIcon()
	s.icon.Store(&iconCache{
		contentType: contentType,
		data:        data,
		fetchedAt:   time.Now(),
	})

	return contentType, data
}

func (s *Server) fetchIcon() (string, []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), iconTimeout)
	defer cancel()

	hc := &http.Client{Transport: s.rt}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0"},
		nil,
	)

	session, err := client.Connect(
		ctx,
		&mcp.StreamableClientTransport{
			Endpoint:   s.url.String(),
			HTTPClient: hc,
		},
		nil,
	)

	if err != nil {
		return "", nil
	}

	defer session.Close()

	result := session.InitializeResult()
	if result == nil || result.ServerInfo == nil {
		return "", nil
	}

	icons := slices.Clone(result.ServerInfo.Icons)
	slices.SortStableFunc(icons, func(a, b mcp.Icon) int {
		return iconPriority(a.MIMEType) - iconPriority(b.MIMEType)
	})

	for _, icon := range icons {
		if contentType, data, ok := resolveIcon(hc, icon); ok {
			return contentType, data
		}
	}

	return "", nil
}

func iconPriority(mimeType string) int {
	switch strings.ToLower(mimeType) {
	case "image/svg+xml":
		return 0
	case "image/png":
		return 1
	case "image/webp":
		return 2
	case "image/jpeg", "image/jpg":
		return 3
	case "image/x-icon", "image/vnd.microsoft.icon":
		return 4
	default:
		return 5
	}
}

func resolveIcon(hc *http.Client, icon mcp.Icon) (string, []byte, bool) {
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
