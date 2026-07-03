package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/moderations" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}

		if body["model"] != "omni-moderation-latest" {
			t.Errorf("unexpected model: %v", body["model"])
		}

		if body["input"] != "some text" {
			t.Errorf("unexpected input: %v", body["input"])
		}

		w.Header().Set("Content-Type", "application/json")

		w.Write([]byte(`{
			"id": "modr-123",
			"model": "omni-moderation-latest",
			"results": [
				{
					"flagged": true,
					"categories": {
						"violence": true,
						"harassment": true,
						"hate": false
					},
					"category_scores": {
						"violence": 0.91,
						"harassment": 0.42,
						"hate": 0.01
					}
				}
			]
		}`))
	}))

	defer server.Close()

	client, err := New(server.URL + "/v1")

	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Check(context.Background(), "some text", nil)

	if err != nil {
		t.Fatal(err)
	}

	if !result.Flagged {
		t.Error("expected result to be flagged")
	}

	if len(result.Categories) != 2 {
		t.Fatalf("unexpected categories: %v", result.Categories)
	}

	if result.Categories[0].Name != "violence" || result.Categories[0].Score != 0.91 {
		t.Errorf("unexpected category: %v", result.Categories[0])
	}

	if result.Categories[1].Name != "harassment" || result.Categories[1].Score != 0.42 {
		t.Errorf("unexpected category: %v", result.Categories[1])
	}
}
