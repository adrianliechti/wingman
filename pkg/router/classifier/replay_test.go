package classifier

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestReplayReportsAccuracyAndDriftWithoutServing(t *testing.T) {
	cheap := &mockCompleter{name: "cheap"}
	strong := &mockCompleter{name: "strong"}
	c, err := NewCompleter([]Candidate{
		{Completer: cheap, Model: "cheap", Cost: 1, MaxDifficulty: 2},
		{Completer: strong, Model: "strong", Cost: 5, MaxDifficulty: 4},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	report := c.Replay(context.Background(), []ReplayCase{
		{ID: "easy", Messages: userMsg("hello"), ExpectedModel: "cheap"},
		{ID: "hard", Messages: userMsg("hello"), ExpectedModel: "strong"},
		{ID: "unlabeled", Messages: userMsg("debug the distributed race condition"), Options: reasoning(provider.EffortXHigh)},
	})

	if report.Total != 3 || report.Labeled != 2 || report.Correct != 1 || report.Accuracy != 0.5 {
		t.Fatalf("unexpected summary: %+v", report)
	}
	if report.UnderRouted != 1 || report.OverRouted != 0 || report.MeanCostDelta != -2 {
		t.Fatalf("unexpected drift: %+v", report)
	}
	if cheap.calls != 0 || strong.calls != 0 {
		t.Fatalf("replay must not serve candidates: cheap=%d strong=%d", cheap.calls, strong.calls)
	}
}
