package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// query layers JSON-RPC 2.0 client semantics on top of the transport:
//   - send(method, params) → wait for response by id
//   - notify(method, params) → fire-and-forget
//   - dispatch incoming notifications and server-requests to a handler
type query struct {
	t *transport

	counter atomic.Int64

	mu      sync.Mutex
	pending map[string]chan jsonrpcMessage

	notifications chan jsonrpcMessage
	requests      chan jsonrpcMessage

	dispatchCtx    context.Context
	dispatchCancel context.CancelFunc
	dispatchDone   chan struct{}
}

func newQuery(t *transport) *query {
	q := &query{
		t:             t,
		pending:       make(map[string]chan jsonrpcMessage),
		notifications: make(chan jsonrpcMessage, 256),
		requests:      make(chan jsonrpcMessage, 32),
		dispatchDone:  make(chan struct{}),
	}
	q.dispatchCtx, q.dispatchCancel = context.WithCancel(context.Background())
	go q.dispatch()
	return q
}

// stop terminates the dispatcher (called by the completer on teardown).
func (q *query) stop() {
	q.dispatchCancel()
	<-q.dispatchDone
}

func (q *query) dispatch() {
	defer close(q.dispatchDone)
	defer close(q.notifications)
	defer close(q.requests)

	for {
		select {
		case msg, ok := <-q.t.out:
			if !ok {
				return
			}
			switch {
			case len(msg.ID) > 0 && msg.Method != "":
				// inbound request from server
				select {
				case q.requests <- msg:
				case <-q.dispatchCtx.Done():
					return
				}
			case len(msg.ID) > 0:
				// response to one of our requests
				q.routeResponse(msg)
			default:
				// notification (no id)
				select {
				case q.notifications <- msg:
				case <-q.dispatchCtx.Done():
					return
				}
			}
		case <-q.dispatchCtx.Done():
			return
		}
	}
}

func (q *query) routeResponse(msg jsonrpcMessage) {
	id := string(msg.ID)
	q.mu.Lock()
	ch, ok := q.pending[id]
	if ok {
		delete(q.pending, id)
	}
	q.mu.Unlock()

	if !ok {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

func (q *query) nextID() (string, json.RawMessage) {
	n := q.counter.Add(1)
	s := strconv.FormatInt(n, 10)
	return s, json.RawMessage(s)
}

// call sends a JSON-RPC request and blocks until a response arrives or
// ctx/timeout fires. The result field of the response is returned raw.
func (q *query) call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	idStr, idRaw := q.nextID()

	var paramsRaw json.RawMessage
	if params != nil {
		buf, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		paramsRaw = buf
	}

	ch := make(chan jsonrpcMessage, 1)
	q.mu.Lock()
	q.pending[idStr] = ch
	q.mu.Unlock()

	defer func() {
		q.mu.Lock()
		delete(q.pending, idStr)
		q.mu.Unlock()
	}()

	req := jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      idRaw,
		Method:  method,
		Params:  paramsRaw,
	}

	if err := q.t.send(req); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("codex: %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	case <-timer.C:
		return nil, fmt.Errorf("codex: %s: timed out", method)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (q *query) notify(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		buf, err := json.Marshal(params)
		if err != nil {
			return err
		}
		paramsRaw = buf
	}
	msg := jsonrpcMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsRaw,
	}
	return q.t.send(msg)
}

// reply answers a server-initiated request with a successful result.
func (q *query) reply(id json.RawMessage, result any) error {
	buf, err := json.Marshal(result)
	if err != nil {
		return err
	}
	msg := jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  buf,
	}
	return q.t.send(msg)
}

// replyError answers a server-initiated request with an error.
func (q *query) replyError(id json.RawMessage, code int, message string) error {
	msg := jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: message},
	}
	return q.t.send(msg)
}
