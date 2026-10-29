package budget

import (
	"context"
	"sync"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type realtime struct {
	reporter
	provider.Realtime
}

func NewRealtime(name, model string, p provider.Realtime, b Provider) provider.Realtime {
	return &realtime{reporter{b, name, model}, p}
}

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
	s := &realtimeSession{RealtimeSession: session, reporter: p.reporter, ctx: ctx, report: r, events: make(chan provider.RealtimeEvent, 16), stop: make(chan struct{}), done: make(chan struct{})}
	go s.relay()
	return s, nil
}

type realtimeSession struct {
	provider.RealtimeSession
	reporter
	ctx        context.Context
	report     *Report
	events     chan provider.RealtimeEvent
	stop, done chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

func (s *realtimeSession) Events() <-chan provider.RealtimeEvent { return s.events }

func (s *realtimeSession) relay() {
	defer close(s.done)
	defer close(s.events)
	// Realtime usage snapshots are cumulative per response, not per session.
	usage := map[string]*provider.Usage{}
	defer func() {
		var total *provider.Usage
		for _, u := range usage {
			if total == nil {
				total = &provider.Usage{}
			}
			total.InputTokens += u.InputTokens
			total.OutputTokens += u.OutputTokens
		}
		s.report.observe("", total)
		s.finish(s.ctx, s.report)
	}()
	for {
		select {
		case <-s.stop:
			return
		case event, ok := <-s.RealtimeSession.Events():
			if !ok {
				return
			}
			if event.Usage != nil {
				u := usage[event.ResponseID]
				if u == nil {
					u = &provider.Usage{}
					usage[event.ResponseID] = u
				}
				u.Merge(&provider.Usage{InputTokens: event.Usage.InputTokens, OutputTokens: event.Usage.OutputTokens})
			}
			select {
			case s.events <- event:
			case <-s.stop:
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
