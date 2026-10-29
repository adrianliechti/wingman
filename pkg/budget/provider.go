package budget

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"
)

var ErrMissingUser = errors.New("budget requires an authenticated user ID")

type systemUsageKey struct{}

// SystemContext excludes shared infrastructure work, such as classifier
// centroid embeddings, from per-user admission and accounting.
func SystemContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemUsageKey{}, true)
}

type reporter struct {
	budget Provider
	name   string
	model  string
}

func (p reporter) start(ctx context.Context) (*Report, error) {
	if system, _ := ctx.Value(systemUsageKey{}).(bool); system {
		return nil, nil
	}
	user, _ := ctx.Value(auth.UserContextKey).(string)
	if user == "" {
		return nil, &provider.ProviderError{Code: http.StatusUnauthorized, Type: "authentication_error", Message: ErrMissingUser.Error(), Err: ErrMissingUser}
	}
	report := &Report{OperationID: uuid.NewString(), UserID: user, Provider: p.name, Model: p.model}
	admitted := false
	if queue, ok := p.budget.(*Async); ok {
		if err := queue.reserve(report.OperationID); err != nil {
			return nil, &provider.ProviderError{Code: http.StatusServiceUnavailable, Type: "budget_unavailable", Message: err.Error(), Err: err}
		}
		defer func() {
			if !admitted {
				queue.release(report.OperationID)
			}
		}()
	}
	result, err := p.budget.Check(ctx, user)
	if err != nil {
		return nil, &provider.ProviderError{Code: http.StatusServiceUnavailable, Type: "budget_unavailable", Message: "budget service unavailable", Err: err}
	}
	if result == nil {
		return nil, &provider.ProviderError{Code: http.StatusServiceUnavailable, Type: "budget_unavailable", Message: "budget service returned no admission decision"}
	}
	if !result.Allowed {
		message := result.Message
		if message == "" {
			message = "user budget exhausted"
		}
		return nil, &provider.ProviderError{Code: http.StatusTooManyRequests, Type: "insufficient_quota", Message: message, RetryAfter: max(0, time.Until(result.ResetsAt))}
	}
	admitted = true
	return report, nil
}

func (p reporter) finish(ctx context.Context, report *Report) {
	if report == nil {
		return
	}
	report.CompletedAt = time.Now().UTC()
	// A disconnected caller must not cancel accounting. The budget transport
	// bounds delivery with its own timeout, including any report retries.
	if err := p.budget.Report(context.WithoutCancel(ctx), report); err != nil {
		slog.ErrorContext(ctx, "budget report failed", "operation_id", report.OperationID, "provider", report.Provider, "model", report.Model, "error", err)
	}
}

func (r *Report) observe(model string, usage *provider.Usage) {
	if r == nil {
		return
	}
	if model != "" {
		r.Model = model
	}
	if usage == nil {
		return
	}
	if r.Usage == nil {
		r.Usage = &Usage{}
	}
	r.Usage.Merge(usage)
}

type completer struct {
	reporter
	provider.Completer
}

// NewCompleter wraps an upstream model, not a router or agent. Each invocation
// emits one report when iteration ends, including errors and early stops.
func NewCompleter(name, model string, p provider.Completer, b Provider) provider.Completer {
	return &completer{reporter{b, name, model}, p}
}

func (p *completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		report, err := p.start(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		defer p.finish(ctx, report)
		for result, err := range p.Completer.Complete(ctx, messages, options) {
			if result != nil {
				report.observe(result.Model, result.Usage)
				if report != nil && result.Tier != "" {
					report.ServiceTier = string(result.Tier)
				}
			}
			if !yield(result, err) || err != nil {
				return
			}
		}
	}
}

type embedder struct {
	reporter
	provider.Embedder
}

func NewEmbedder(name, model string, p provider.Embedder, b Provider) provider.Embedder {
	return &embedder{reporter{b, name, model}, p}
}

func (p *embedder) Embed(ctx context.Context, texts []string, options *provider.EmbedOptions) (*provider.Embedding, error) {
	report, err := p.start(ctx)
	if err != nil {
		return nil, err
	}
	defer p.finish(ctx, report)
	result, err := p.Embedder.Embed(ctx, texts, options)
	if result != nil {
		report.observe(result.Model, result.Usage)
	}
	return result, err
}

type decider struct {
	reporter
	provider.Decider
}

func NewDecider(name, model string, p provider.Decider, b Provider) provider.Decider {
	return &decider{reporter{b, name, model}, p}
}

func (p *decider) Decide(ctx context.Context, input *provider.DecisionInput) (*provider.Decision, error) {
	report, err := p.start(ctx)
	if err != nil {
		return nil, err
	}
	defer p.finish(ctx, report)
	result, err := p.Decider.Decide(ctx, input)
	if result != nil {
		report.observe(result.Model, result.Usage)
	}
	return result, err
}
