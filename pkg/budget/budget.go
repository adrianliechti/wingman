// Package budget integrates an external budget service. The service owns prices,
// limits and accounting; Wingman checks admission and reports observed usage.
package budget

import (
	"context"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type Provider interface {
	Check(ctx context.Context, userID string) (*Result, error)
	// Report must deduplicate by OperationID before recording usage.
	Report(ctx context.Context, report *Report) error
}

type Result struct {
	Allowed  bool
	Message  string
	ResetsAt time.Time
}

type Report struct {
	OperationID string
	UserID      string
	Provider    string
	Model       string
	ServiceTier string
	CompletedAt time.Time
	// Usage is nil when the upstream did not provide token counts.
	Usage *Usage
}

type Usage = provider.Usage
