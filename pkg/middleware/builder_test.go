package middleware

import (
	"context"
	"slices"
	"testing"
)

func TestBuilderOrderAndExplicitBuild(t *testing.T) {
	var calls []string
	compiled := 0
	wrap := func(name string) Middleware[handler] {
		return func(next handler) handler {
			compiled++
			return func(ctx context.Context) string {
				calls = append(calls, name+" before")
				result := next(ctx)
				calls = append(calls, name+" after")
				return result
			}
		}
	}
	b := NewBuilder(handler(func(ctx context.Context) string {
		if ctx != t.Context() {
			t.Error("context changed")
		}
		calls = append(calls, "provider")
		return "result"
	}))
	b.Use(wrap("outer"))
	b.Use(wrap("inner"))
	if compiled != 0 {
		t.Fatal("Use compiled middleware")
	}
	provider := b.Build()
	if compiled != 2 {
		t.Fatalf("compiled = %d", compiled)
	}
	if got := provider(t.Context()); got != "result" {
		t.Fatalf("result = %q", got)
	}
	want := []string{"outer before", "inner before", "provider", "inner after", "outer after"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestBuilderBuildsIndependentChains(t *testing.T) {
	b := NewBuilder(handler(func(context.Context) string { return "provider" }))
	original := b.Build()
	compiled := 0
	b.Use(func(next handler) handler {
		compiled++
		return func(ctx context.Context) string { return "wrapped " + next(ctx) }
	})
	for range 2 {
		if got := b.Build()(t.Context()); got != "wrapped provider" {
			t.Fatalf("result = %q", got)
		}
	}
	if got := original(t.Context()); got != "provider" {
		t.Fatalf("original changed: %q", got)
	}
	if compiled != 2 {
		t.Fatalf("compiled = %d", compiled)
	}
}

func TestEmptyBuilderPreservesProvider(t *testing.T) {
	provider := new(int)
	if got := NewBuilder(provider).Build(); got != provider {
		t.Fatal("empty builder changed provider")
	}
}
