package tavily

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

func TestOptionalFullTextPreservesExcerptsAndFailedAttempts(t *testing.T) {
	for _, include := range []bool{false, true} {
		c, err := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var request map[string]any
			if err := json.UnmarshalRead(r.Body, &request); err != nil {
				t.Fatal(err)
			}
			if (request["include_raw_content"] == true) != include {
				t.Fatal("raw content selector was not respected")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[{"url":"https://example.com","content":"Excerpt","raw_content":"Full page"},{"url":"https://example.com/missing","content":"Still useful","raw_content":null}]}`))}, nil
		})}))
		if err != nil {
			t.Fatal(err)
		}
		options := &searcher.SearchOptions{}
		if include {
			options.Text = &searcher.TextOptions{}
		}
		results, err := c.Search(t.Context(), "test", options)
		if err != nil {
			t.Fatal(err)
		}
		if !c.SearchCapabilities().Content || len(results) != 2 || results[0].Excerpt != "Excerpt" || results[1].Excerpt != "Still useful" {
			t.Fatal("excerpts were lost")
		}
		if include {
			if results[0].Text == nil || *results[0].Text != "Full page" || results[1].Text == nil || *results[1].Text != "" {
				t.Fatal("full text or failed retrieval state was lost")
			}
		} else if results[0].Text != nil || results[1].Text != nil {
			t.Fatal("unrequested text was returned")
		}
	}
}
