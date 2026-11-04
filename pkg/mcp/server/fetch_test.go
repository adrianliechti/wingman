package server

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/scraper/exa"
	"github.com/adrianliechti/wingman/pkg/tool/scrape"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fetchRoundTripFunc func(*http.Request) (*http.Response, error)

func (f fetchRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFetchFullExaContentOverMCP(t *testing.T) {
	page := strings.Repeat("Earlier section. ", 1000) + "\nLaunch budget: 83 million credits."
	upstream, err := exa.New("key", exa.WithClient(&http.Client{Transport: fetchRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body struct {
			Text        bool `json:"text"`
			MaxAgeHours int  `json:"maxAgeHours"`
		}
		if err := json.UnmarshalRead(req.Body, &body); err != nil {
			t.Fatal(err)
		}
		if !body.Text || body.MaxAgeHours != 0 {
			t.Fatalf("incomplete extraction request: %+v", body)
		}
		data, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"text": page}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	fetch, err := scrape.New(upstream)
	if err != nil {
		t.Fatal(err)
	}
	session := connectTo(t, newServer(t, "", fetch))
	for _, test := range []struct {
		name      string
		arguments map[string]any
		want      string
	}{
		{"whole page", map[string]any{}, "83 million credits"},
		{"bounded prefix", map[string]any{"max_chars": 6000}, "start_index=6000"},
		{"late passage", map[string]any{"query": "Launch budget", "max_chars": 1500}, "83 million credits"},
		{"continuation", map[string]any{"start_index": 17000, "max_chars": 1500}, "83 million credits"},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.arguments["url"] = "https://source.test/news"
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "web_fetch", Arguments: test.arguments})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || len(result.Content) != 1 {
				t.Fatalf("result=%+v", result)
			}
			text := result.Content[0].(*mcp.TextContent).Text
			if !strings.Contains(text, test.want) {
				t.Fatalf("missing %q in %s", test.want, text)
			}
		})
	}
}
