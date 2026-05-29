package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

const storeTestModel = "store-test-model"

type echoCompleter struct{}

func (echoCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		yield(&provider.Completion{
			Status: provider.CompletionStatusCompleted,
			Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{provider.TextContent("hi")},
			},
		}, nil)
	}
}

func newStoreHandler(t *testing.T) *Handler {
	t.Helper()
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(storeTestModel, echoCompleter{})
	return New(cfg)
}

func postResponses(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	h.handleResponses(rec, req)
	return rec
}

// Store and previous_response_id are unknown to wingman — silently accepted
// and dropped by json.Unmarshal. The response always carries store=false as
// the "we don't persist" signal.

func TestStoreTrueAcceptedAndResponseEchoesStoreFalse(t *testing.T) {
	h := newStoreHandler(t)
	rec := postResponses(t, h, `{
		"model": "`+storeTestModel+`",
		"store": true,
		"input": "hello"
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (store is silently accepted), got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if store, ok := resp["store"].(bool); !ok || store {
		t.Fatalf("expected response.store=false (statelessness signal), got %v", resp["store"])
	}
}

func TestStoreOmittedAccepted(t *testing.T) {
	h := newStoreHandler(t)
	rec := postResponses(t, h, `{
		"model": "`+storeTestModel+`",
		"input": "hello"
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if store, ok := resp["store"].(bool); !ok || store {
		t.Fatalf("expected response.store=false, got %v", resp["store"])
	}
}

func TestPreviousResponseIDAcceptedButIgnored(t *testing.T) {
	h := newStoreHandler(t)
	rec := postResponses(t, h, `{
		"model": "`+storeTestModel+`",
		"previous_response_id": "resp_abc123",
		"input": "hello"
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (previous_response_id is silently accepted), got %d: %s", rec.Code, rec.Body.String())
	}

	// The response's previous_response_id is null since wingman doesn't track it.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if prev, ok := resp["previous_response_id"]; ok && prev != nil {
		t.Fatalf("expected response.previous_response_id=null, got %v", prev)
	}
}
