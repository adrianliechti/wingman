package server

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const progressInterval = 250 * time.Millisecond

func executeTool(ctx context.Context, req *mcp.CallToolRequest, p tool.Provider, name string, args map[string]any) (any, error) {
	token := req.Params.GetProgressToken()
	if !validProgressToken(token) {
		return p.Execute(ctx, name, args)
	}

	// Keep all state local to this call, including for stateless HTTP sessions.
	// Serialize iterator updates and timer flushes so progress stays ordered.
	var mu sync.Mutex
	var progress float64
	var last time.Time
	var pending string
	send := func(message string) {
		progress++
		last = time.Now()
		// Progress is best effort; a notification failure must not discard a report.
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      progress,
			Message:       message,
		})
	}
	notify := func(message string) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if time.Since(last) < progressInterval {
			pending = message
			return
		}
		pending = ""
		send(message)
	}
	// Flush coalesced stages even when the next iterator step takes a long time.
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				if ctx.Err() == nil && pending != "" && time.Since(last) >= progressInterval {
					send(pending)
					pending = ""
				}
				mu.Unlock()
			}
		}
	}()
	defer func() {
		close(done)
		<-stopped
		// The ticker has exited, so the latest coalesced stage can be sent
		// without locking, still before the tool result.
		if pending != "" && ctx.Err() == nil {
			send(pending)
		}
	}()

	return tool.Collect(tool.ExecuteStream(ctx, p, name, args), notify)
}

func validProgressToken(token any) bool {
	switch token := token.(type) {
	case string, int, int32, int64:
		return true
	case float64: // JSON numbers decode to float64 in request metadata.
		return !math.IsNaN(token) && !math.IsInf(token, 0) && math.Trunc(token) == token
	default:
		return false
	}
}
