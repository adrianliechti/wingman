package api

import (
	"encoding/base64"
	"encoding/json/v2"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/auth"
)

type tokenInfo struct {
	Token string `json:"token,omitzero"`

	Header  map[string]any `json:"header,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`

	User   string   `json:"user,omitzero"`
	Email  string   `json:"email,omitzero"`
	Name   string   `json:"name,omitzero"`
	Groups []string `json:"groups,omitempty"`

	Headers http.Header `json:"headers,omitzero"`
}

func (h *Handler) handleToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	info := tokenInfo{
		Headers: r.Header,
	}

	if v, ok := ctx.Value(auth.UserContextKey).(string); ok {
		info.User = v
	}

	if v, ok := ctx.Value(auth.EmailContextKey).(string); ok {
		info.Email = v
	}

	if v, ok := ctx.Value(auth.NameContextKey).(string); ok {
		info.Name = v
	}

	if v, ok := ctx.Value(auth.GroupsContextKey).([]string); ok {
		info.Groups = v
	}

	if token := requestToken(r); token != "" {
		info.Token = token

		parts := strings.Split(token, ".")

		if len(parts) == 3 {
			info.Header = decodeSegment(parts[0])
			info.Payload = decodeSegment(parts[1])
		}
	}

	writeJson(w, info)
}

func requestToken(r *http.Request) string {
	if header := r.Header.Get("Authorization"); header != "" {
		if token, ok := strings.CutPrefix(header, "Bearer "); ok {
			return strings.TrimSpace(token)
		}
	}

	headers := []string{
		"X-Forwarded-Access-Token",
		"X-Auth-Request-Access-Token",
		"X-Auth-Request-Id-Token",
	}

	for _, h := range headers {
		if token := strings.TrimSpace(r.Header.Get(h)); token != "" {
			return token
		}
	}

	return ""
}

func decodeSegment(seg string) map[string]any {
	data, err := base64.RawURLEncoding.DecodeString(seg)

	if err != nil {
		return nil
	}

	var claims map[string]any

	if err := json.Unmarshal(data, &claims); err != nil {
		return nil
	}

	return claims
}
