package otel

import (
	"context"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel/sdk/resource"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func setupHTTP(_ context.Context, _ *resource.Resource) error {
	http.DefaultTransport = Transport(http.DefaultTransport)

	// provider.DefaultClient bypasses http.DefaultTransport; instrument it as
	// well so upstream LLM calls keep their HTTP spans
	provider.DefaultClient.Transport = Transport(provider.DefaultClient.Transport)

	return nil
}

func Transport(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}

	return otelhttp.NewTransport(rt)
}
