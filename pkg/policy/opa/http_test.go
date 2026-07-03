package opa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/policy"
)

func TestClientVerify(t *testing.T) {
	var gotPath string
	var gotInput map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path

		var body struct {
			Input map[string]any `json:"input"`
		}

		json.NewDecoder(r.Body).Decode(&body)
		gotInput = body.Input

		if body.Input["user"] == "adrian" {
			json.NewEncoder(w).Encode(map[string]any{"result": true})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{})
	}))

	defer server.Close()

	client, err := NewClient(server.URL + "/")

	if err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), auth.UserContextKey, "adrian")

	if err := client.Verify(ctx, policy.ResourceModel, "gpt-4o", policy.ActionAccess); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}

	if gotPath != "/v1/data/wingman/allow" {
		t.Fatalf("unexpected path: %s", gotPath)
	}

	if gotInput["resource"] != "model" || gotInput["id"] != "gpt-4o" || gotInput["action"] != "access" {
		t.Fatalf("unexpected input: %v", gotInput)
	}

	if err := client.Verify(context.Background(), policy.ResourceModel, "gpt-4o", policy.ActionAccess); !errors.Is(err, policy.ErrAccessDenied) {
		t.Fatalf("expected access denied, got %v", err)
	}
}
