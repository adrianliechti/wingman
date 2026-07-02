package multi

import (
	"context"
	"errors"
	"testing"

	"github.com/adrianliechti/wingman/pkg/guard"
)

type fakeGuard struct {
	result *guard.Result
	err    error
}

func (g *fakeGuard) Check(ctx context.Context, text string, options *guard.CheckOptions) (*guard.Result, error) {
	return g.result, g.err
}

func TestCheck(t *testing.T) {
	moderation := &fakeGuard{
		result: &guard.Result{
			Flagged: true,

			Categories: []guard.Category{
				{Name: "violence", Score: 0.91},
				{Name: "harassment", Score: 0.42},
			},
		},
	}

	pii := &fakeGuard{
		result: &guard.Result{
			Categories: []guard.Category{
				{Name: "violence", Score: 0.10},
				{Name: "email", Score: 0.99},
			},
		},
	}

	result, err := New(moderation, pii).Check(context.Background(), "some text", nil)

	if err != nil {
		t.Fatal(err)
	}

	if !result.Flagged {
		t.Error("expected result to be flagged")
	}

	if len(result.Categories) != 3 {
		t.Fatalf("unexpected categories: %v", result.Categories)
	}

	if result.Categories[0].Name != "email" || result.Categories[0].Score != 0.99 {
		t.Errorf("unexpected category: %v", result.Categories[0])
	}

	if result.Categories[1].Name != "violence" || result.Categories[1].Score != 0.91 {
		t.Errorf("unexpected category: %v", result.Categories[1])
	}

	if result.Categories[2].Name != "harassment" || result.Categories[2].Score != 0.42 {
		t.Errorf("unexpected category: %v", result.Categories[2])
	}
}

func TestCheckError(t *testing.T) {
	ok := &fakeGuard{
		result: &guard.Result{},
	}

	failed := &fakeGuard{
		err: errors.New("unavailable"),
	}

	if _, err := New(ok, failed).Check(context.Background(), "some text", nil); err == nil {
		t.Error("expected error")
	}
}
