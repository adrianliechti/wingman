package budget

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Async reserves accounting capacity before an upstream operation starts.
// Slow reporting cannot delay response completion; saturation rejects new
// operations rather than dropping reports or creating unbounded goroutines.
type Async struct {
	Provider
	mu         sync.Mutex
	pending    map[string]bool // false: reserved; true: queued
	capacity   int
	closed     bool
	jobs       chan reportJob
	operations sync.WaitGroup
	workers    sync.WaitGroup
	shutdown   sync.Once
	done       chan struct{}
	err        error
}

type reportJob struct {
	ctx    context.Context
	report *Report
}

var errReportingUnavailable = errors.New("budget reporting capacity unavailable")

func NewAsync(p Provider, capacity, workers int) *Async {
	if capacity < 1 || workers < 1 {
		panic("budget queue capacity and workers must be positive")
	}
	a := &Async{Provider: p, capacity: capacity, pending: make(map[string]bool), jobs: make(chan reportJob, capacity), done: make(chan struct{})}
	for range workers {
		a.workers.Add(1)
		go func() {
			defer a.workers.Done()
			for job := range a.jobs {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(job.ctx), 30*time.Second)
				err := a.Provider.Report(ctx, job.report)
				cancel()
				if err != nil {
					slog.ErrorContext(ctx, "budget report failed", "operation_id", job.report.OperationID, "error", err)
				}
				a.release(job.report.OperationID)
			}
		}()
	}
	return a
}

func (a *Async) reserve(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || len(a.pending) >= a.capacity {
		return errReportingUnavailable
	}
	if _, exists := a.pending[id]; exists {
		return errors.New("duplicate budget operation")
	}
	a.pending[id] = false
	a.operations.Add(1)
	return nil
}

func (a *Async) release(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.pending[id]; exists {
		delete(a.pending, id)
		a.operations.Done()
	}
}

func (a *Async) Report(ctx context.Context, report *Report) error {
	if report == nil {
		return errors.New("missing budget report")
	}
	a.mu.Lock()
	queued, exists := a.pending[report.OperationID]
	if !exists {
		a.mu.Unlock()
		return errors.New("budget report has no reserved capacity")
	}
	if queued {
		a.mu.Unlock()
		return nil
	}
	a.pending[report.OperationID] = true
	// Capacity includes in-flight operations and active workers, so enqueue
	// cannot block. Copy mutable usage because the producer relinquishes it.
	copyReport := *report
	if report.Usage != nil {
		copyReport.Usage = &Usage{}
		copyReport.Usage.Merge(report.Usage)
	}
	a.jobs <- reportJob{context.WithoutCancel(ctx), &copyReport}
	a.mu.Unlock()
	return nil
}

func (a *Async) Shutdown(ctx context.Context) error {
	a.shutdown.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		go func() {
			a.operations.Wait()
			close(a.jobs)
			a.workers.Wait()
			if closer, ok := a.Provider.(io.Closer); ok {
				a.err = closer.Close()
			}
			close(a.done)
		}()
	})
	select {
	case <-a.done:
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Async) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.Shutdown(ctx)
}
