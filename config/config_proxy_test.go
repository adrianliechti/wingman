package config

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestProxyClientWithoutURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	client, err := (&proxyConfig{URL: ""}).proxyClient()
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestSearcherProxyPropagation(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	fixtureBaggage, err := baggage.Parse("fixture=test")
	if err != nil {
		t.Fatal(err)
	}
	tracer := sdktrace.NewTracerProvider().Tracer("test")
	ctx, span := tracer.Start(baggage.ContextWithBaggage(t.Context(), fixtureBaggage), "search")
	defer span.End()

	for _, test := range []struct {
		searcherType string
		propagates   bool
	}{
		{searcherType: "Parallel", propagates: false},
		{searcherType: "duckduckgo", propagates: true},
	} {
		t.Run(test.searcherType, func(t *testing.T) {
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (r.Header.Get("Traceparent") != "") != test.propagates {
					t.Errorf("trace context propagation = %v, want %v", !test.propagates, test.propagates)
				}
				if (r.Header.Get("Baggage") != "") != test.propagates {
					t.Errorf("baggage propagation = %v, want %v", !test.propagates, test.propagates)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer proxy.Close()

			settings := searcherConfig{Type: test.searcherType, Proxy: &proxyConfig{URL: proxy.URL}}
			client, err := settings.proxyClient()
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://mcp.example/mcp", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		})
	}
}
