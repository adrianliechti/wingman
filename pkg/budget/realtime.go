package budget

import (
	"context"
	"log/slog"
	"sync"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"
)

type realtime struct {
	reporter
	provider.Realtime
}

func NewRealtime(name, model string, p provider.Realtime, b Provider) provider.Realtime {
	return &realtime{reporter{b, name, model}, p}
}

// Connect admits the session once. Each finished response is reported as its
// own operation, so usage reaches the budget service while the session runs,
// and the user's budget is checked again before the session continues.
func (p *realtime) Connect(ctx context.Context, options *provider.RealtimeOptions) (provider.RealtimeSession, error) {
	r, err := p.start(ctx)
	if err != nil {
		return nil, err
	}
	session, err := p.Realtime.Connect(ctx, options)
	if err != nil {
		p.finish(ctx, r)
		return nil, err
	}
	s := &realtimeSession{RealtimeSession: session, reporter: p.reporter, ctx: ctx, session: r,
		events: make(chan provider.RealtimeEvent, 16), denied: make(chan error, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go s.relay()
	return s, nil
}

type realtimeSession struct {
	provider.RealtimeSession
	reporter
	ctx        context.Context
	session    *Report // nil for system work; holds the session's identity
	events     chan provider.RealtimeEvent
	denied     chan error
	checking   bool
	reported   bool
	stop, done chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

func (s *realtimeSession) Events() <-chan provider.RealtimeEvent { return s.events }

// report sends one operation; nil usage means the provider reported none.
func (s *realtimeSession) report(usage *provider.RealtimeUsage) {
	if s.session == nil {
		return
	}
	r := *s.session
	r.OperationID = uuid.NewString()
	if usage != nil {
		r.observe("", &provider.Usage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens})
	}
	s.deliver(s.ctx, &r)
	s.reported = true
}

// recheck asks the budget service again without blocking event delivery. Only
// an explicit denial ends the session; an unreachable service does not.
func (s *realtimeSession) recheck() {
	if s.session == nil || s.checking {
		return
	}
	s.checking = true
	go func() {
		result, err := s.budget.Check(s.ctx, s.session.UserID)
		if err != nil && s.ctx.Err() == nil {
			slog.WarnContext(s.ctx, "budget recheck failed", "error", err)
		}
		if err != nil || result == nil {
			s.denied <- nil
			return
		}
		s.denied <- denial(result)
	}()
}

func (s *realtimeSession) relay() {
	defer close(s.done)
	defer close(s.events)
	// Usage snapshots are cumulative per response. Usage without a response ID
	// belongs to a turn that produced no response and is reported directly.
	usage := map[string]*provider.RealtimeUsage{}
	defer func() {
		for _, u := range usage {
			s.report(u)
		}
		// A session without any usage is still reported, as unknown usage.
		if !s.reported {
			s.report(nil)
		}
		if t, ok := s.budget.(tracker); ok && s.session != nil {
			t.end()
		}
	}()
	send := func(event provider.RealtimeEvent) bool {
		select {
		case s.events <- event:
			return true
		case <-s.stop:
			return false
		}
	}
	for {
		select {
		case <-s.stop:
			return
		case err := <-s.denied:
			s.checking = false
			if err != nil {
				send(provider.RealtimeEvent{Type: provider.RealtimeEventError, Err: err,
					Error: &provider.RealtimeError{Type: "insufficient_quota", Code: "insufficient_quota", Message: err.Error()}})
				return
			}
		case event, ok := <-s.RealtimeSession.Events():
			if !ok {
				return
			}
			if u := event.Usage; u != nil {
				if event.ResponseID == "" {
					s.report(u)
					s.recheck()
				} else {
					total := usage[event.ResponseID]
					if total == nil {
						total = &provider.RealtimeUsage{}
						usage[event.ResponseID] = total
					}
					total.InputTokens = max(total.InputTokens, u.InputTokens)
					total.OutputTokens = max(total.OutputTokens, u.OutputTokens)
				}
			}
			if event.Type == provider.RealtimeEventResponseDone {
				if total, ok := usage[event.ResponseID]; ok {
					delete(usage, event.ResponseID)
					s.report(total)
				}
				s.recheck()
			}
			if !send(event) {
				return
			}
		}
	}
}

func (s *realtimeSession) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.closeErr = s.RealtimeSession.Close()
		<-s.done
	})
	return s.closeErr
}
