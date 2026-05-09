package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxLineBuffer = 16 * 1024 * 1024 // 16 MiB cap

	gracefulShutdown = 5 * time.Second
	termGrace        = 5 * time.Second
)

// transport owns the codex-app-server subprocess and the stdin/stdout NDJSON
// JSON-RPC framing. It emits parsed messages on out and accepts JSON-
// marshallable values via send.
type transport struct {
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout io.ReadCloser

	stderr *safeBuffer

	writeMu sync.Mutex

	out chan jsonrpcMessage

	closeOnce sync.Once
	closed    chan struct{}

	readDone chan struct{}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startTransport(ctx context.Context, command string, args []string, env []string, cwd string) (*transport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = env
	cmd.Dir = cwd

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("codex: stdout: %w", err)
	}

	stderr := &safeBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("codex: start %s: %w", command, err)
	}

	t := &transport{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   stderr,
		out:      make(chan jsonrpcMessage, 64),
		closed:   make(chan struct{}),
		readDone: make(chan struct{}),
	}

	go t.readLoop()

	return t, nil
}

// readLoop scans stdout line-by-line, accumulating partial JSON across line
// breaks (the server writes one object per line, but very large lines can be
// chunked by the OS pipe).
func (t *transport) readLoop() {
	defer close(t.readDone)
	defer close(t.out)

	scanner := bufio.NewScanner(t.stdout)
	scanner.Buffer(make([]byte, 1024*1024), maxLineBuffer)

	var buffer strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Drop pre-JSON noise only when we're not already mid-object.
		if buffer.Len() == 0 && !strings.HasPrefix(line, "{") {
			continue
		}

		if buffer.Len()+len(line) > maxLineBuffer {
			buffer.Reset()
			continue
		}

		buffer.WriteString(line)

		var msg jsonrpcMessage
		if err := json.Unmarshal([]byte(buffer.String()), &msg); err != nil {
			// Wait for more lines to complete the object.
			continue
		}

		buffer.Reset()

		select {
		case t.out <- msg:
		case <-t.closed:
			return
		}
	}
}

// send marshals v to JSON and writes it as a single \n-terminated line.
func (t *transport) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	select {
	case <-t.closed:
		return errors.New("codex: transport closed")
	default:
	}

	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("codex: write: %w", err)
	}
	if _, err := t.stdin.Write([]byte("\n")); err != nil {
		return fmt.Errorf("codex: write: %w", err)
	}

	return nil
}

func (t *transport) closeStdin() {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
}

// close terminates the subprocess and frees pipes. Three-phase shutdown:
// close stdin → SIGTERM → SIGKILL.
func (t *transport) close() {
	t.closeOnce.Do(func() {
		close(t.closed)
		t.closeStdin()

		select {
		case <-t.readDone:
			_ = t.cmd.Wait()
			return
		case <-time.After(gracefulShutdown):
		}

		termSent := false
		if t.cmd.Process != nil {
			if err := t.cmd.Process.Signal(syscall.SIGTERM); err == nil {
				termSent = true
			}
		}
		if termSent {
			select {
			case <-t.readDone:
				_ = t.cmd.Wait()
				return
			case <-time.After(termGrace):
			}
		}

		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		<-t.readDone
		_ = t.cmd.Wait()
	})
}

func (t *transport) stderrText() string {
	return t.stderr.String()
}
