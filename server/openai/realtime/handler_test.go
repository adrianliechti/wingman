package realtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
)

func TestRealtimeRequiresConfiguredProvider(t *testing.T) {
	// These variables used to activate the deleted compatibility proxy. They
	// must no longer make an unknown model reachable.
	t.Setenv("OPENAI_API_KEY", "legacy-key")
	t.Setenv("REALTIME_API_KEY", "legacy-realtime-key")
	t.Setenv("REALTIME_BASE_URL", "https://example.com/v1")

	request := httptest.NewRequest(http.MethodGet, "/realtime?model=missing", nil)
	response := httptest.NewRecorder()
	New(&config.Config{}).handleRealtime(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if !strings.Contains(response.Body.String(), "realtime provider not found: missing") {
		t.Fatalf("body = %q", response.Body.String())
	}
}
