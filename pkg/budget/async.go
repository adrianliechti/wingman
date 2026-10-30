package budget

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Async delivers reports in the background so slow accounting cannot delay
// responses. The queue bounds memory, not concurrency: a full queue makes the
// reporting call wait for a worker instead of rejecting work or dropping usage.
// Shutdown waits for in-flight operations, so their reports are still sent.
type Async struct {
	Provider
	mu       sync.Mutex
	active   int  // operations between begin and end
	closing  bool // shutdown requested; jobs closes once active reaches zero
	stopped  bool // jobs is closed
	jobs     chan reportJob
	workers  sync.WaitGroup
	shutdown sync.Once
	done     chan struct{}
	err      error
}

type reportJob struct {
	ctx    context.Context
	report *Report
}

var errReportingUnavailable = errors.New("budget reporting has shut down")

func NewAsync(p Provider, capacity, workers int) *Async {
	if capacity < 1 || workers < 1 {
		panic("budget queue capacity and workers must be positive")
	}
	a := &Async{Provider: p, jobs: make(chan reportJob, capacity), done: make(chan struct{})}
	for range workers {
		a.workers.Add(1)
		go func() {
			defer a.workers.Done()
			for job := range a.jobs {
				a.deliver(job)
			}
		}()
	}
	return a
}

func (a *Async) deliver(job reportJob) {
	ctx, cancel := context.WithTimeout(job.ctx, 30*time.Second)
	defer cancel()
	if err := a.Provider.Report(ctx, job.report); err != nil {
		slog.ErrorContext(ctx, "budget report failed", "operation_id", job.report.OperationID, "error", err)
	}
}

func (a *Async) begin() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return errReportingUnavailable
	}
	a.active++
	return nil
}

func (a *Async) end() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active--
	a.stopIdle()
}

// stopIdle closes the queue once shutdown has started and no operation can
// enqueue anymore. The caller holds mu.
func (a *Async) stopIdle() {
	if a.closing && a.active == 0 && !a.stopped {
		a.stopped = true
		close(a.jobs)
	}
}

func (a *Async) Report(ctx context.Context, report *Report) error {
	if report == nil {
		return errors.New("missing budget report")
	}
	// Copy mutable usage because the producer relinquishes it.
	copyReport := *report
	if report.Usage != nil {
		copyReport.Usage = &Usage{}
		copyReport.Usage.Merge(report.Usage)
	}
	job := reportJob{context.WithoutCancel(ctx), &copyReport}
	// Holding an operation keeps the queue open during the send.
	if err := a.begin(); err != nil {
		a.deliver(job)
		return nil
	}
	defer a.end()
	a.jobs <- job
	return nil
}

func (a *Async) Shutdown(ctx context.Context) error {
	a.shutdown.Do(func() {
		a.mu.Lock()
		a.closing = true
		a.stopIdle()
		a.mu.Unlock()
		go func() {
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
