package harness

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecorderRequestLimit(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	r := NewRecorder(t, upstream.URL, nil, 1)
	for _, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		resp, err := http.Post(r.URL+"/responses", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("status %d, want %d", resp.StatusCode, want)
		}
	}
	r.Close()
	if calls != 1 || len(r.Exchanges()) != 2 {
		t.Fatalf("request limit did not bound upstream calls or retain failures: calls=%d exchanges=%d", calls, len(r.Exchanges()))
	}
}
