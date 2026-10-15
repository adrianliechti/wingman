package web_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/server/web"
	"github.com/adrianliechti/wingman/test/webtest"
)

type rejectingGuard struct{}

func (rejectingGuard) Check(context.Context, string, *guard.CheckOptions) (*guard.Result, error) {
	return &guard.Result{Flagged: true}, nil
}

func TestSearchGuardRejectionIsClientError(t *testing.T) {
	for _, dialect := range []string{"exa", "tavily"} {
		t.Run(dialect, func(t *testing.T) {
			s, providers := webtest.NewServer(t, false)
			s.RegisterSearcher("guarded", searcher.NewGuard(rejectingGuard{}, providers["plain"]))
			webtest.Response(t, webtest.Send(t, s, "/v1/"+dialect+"/search", `{"model":"guarded","query":"blocked"}`, nil), http.StatusBadRequest)
			if providers["plain"].Searches != 0 {
				t.Fatal("rejected request reached the search provider")
			}
		})
	}
}

func TestStatusOfPreservesGuardAndUpstreamErrors(t *testing.T) {
	if got := web.StatusOf(fmt.Errorf("search: %w", &guard.FlaggedError{})); got != http.StatusBadRequest {
		t.Fatalf("wrapped rejection status = %d", got)
	}
	if got := web.StatusOf(errors.New("guard unavailable")); got != http.StatusBadGateway {
		t.Fatalf("upstream failure status = %d", got)
	}
}
