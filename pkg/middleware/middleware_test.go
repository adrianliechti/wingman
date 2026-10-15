package middleware

import (
	"context"
	"slices"
	"testing"
)

type handler func(context.Context) string
type contextKey string

func TestChainOrderAndContext(t *testing.T) {
	var calls []string
	wrap := func(name string) Middleware[handler] {
		return func(next handler) handler {
			return func(ctx context.Context) string {
				calls = append(calls, name+" before")
				result := next(context.WithValue(ctx, contextKey(name), name))
				calls = append(calls, name+" after")
				return result
			}
		}
	}
	terminal := handler(func(ctx context.Context) string {
		if ctx.Value(contextKey("outer")) != "outer" || ctx.Value(contextKey("inner")) != "inner" {
			t.Fatal("middleware context was lost")
		}
		calls = append(calls, "provider")
		return "result"
	})
	if got := Chain(terminal, wrap("outer"), wrap("inner"))(t.Context()); got != "result" {
		t.Fatalf("result = %q", got)
	}
	want := []string{"outer before", "inner before", "provider", "inner after", "outer after"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestChainShortCircuit(t *testing.T) {
	terminal := handler(func(context.Context) string {
		t.Fatal("provider called")
		return ""
	})
	block := func(next handler) handler {
		return func(context.Context) string { return "blocked" }
	}
	if got := Chain(terminal, block)(t.Context()); got != "blocked" {
		t.Fatalf("result = %q", got)
	}
}

func TestEmptyChainPreservesProvider(t *testing.T) {
	provider := new(int)
	if Chain(provider) != provider {
		t.Fatal("empty chain changed provider")
	}
}
