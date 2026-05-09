package claude

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
	maxLineBuffer = 16 * 1024 * 1024 // 16 MiB cap, matches Python SDK default

	// gracefulShutdown is how long we let the CLI flush its session
	// transcript and exit naturally after we close stdin. The CLI writes
	// the session store on EOF; killing too early can leave a partial
	// transcript and break the next `--resume` call.
	gracefulShutdown = 5 * time.Second

	// termGrace is how long we give the CLI to react to SIGTERM before
	// escalating to SIGKILL.
	termGrace = 5 * time.Second
)

// transport owns the CLI subprocess and the stdin/stdout NDJSON framing. It
// emits parsed envelopes on out and accepts JSON-marshallable frames via send.
//
// Mirrors claude_agent_sdk._internal.transport.subprocess_cli.SubprocessCLITransport.
type transport struct {
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout io.ReadCloser

	stderr *safeBuffer

	writeMu sync.Mutex

	out chan envelope

	closeOnce sync.Once
	closed    chan struct{}

	readDone chan struct{}
}

// safeBuffer is a mutex-protected bytes.Buffer. exec.Cmd's stderr-copy
// goroutine writes to it while the streaming loop reads via stderrText() —
// a plain bytes.Buffer would race.
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
		return nil, fmt.Errorf("claude: stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("claude: stdout: %w", err)
	}

	stderr := &safeBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("claude: start %s: %w", command, err)
	}

	t := &transport{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   stderr,
		out:      make(chan envelope, 64),
		closed:   make(chan struct{}),
		readDone: make(chan struct{}),
	}

	go t.readLoop()

	return t, nil
}

// readLoop scans stdout line-by-line, accumulating partial JSON across line
// breaks (the CLI writes one object per line, but very large lines can be
// chunked by the OS pipe). Mirrors subprocess_cli._read_messages_impl().
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

		// Drop pre-JSON noise (e.g. "[SandboxDebug] …") only when we're not
		// already mid-object — same heuristic the Python SDK uses (#347).
		if buffer.Len() == 0 && !strings.HasPrefix(line, "{") {
			continue
		}

		if buffer.Len()+len(line) > maxLineBuffer {
			// A malformed line is preventing the buffer from clearing; drop
			// what we have rather than grow without bound.
			buffer.Reset()
			continue
		}

		buffer.WriteString(line)

		var env envelope
		if err := json.Unmarshal([]byte(buffer.String()), &env); err != nil {
			// Wait for more lines to complete the object.
			continue
		}

		buffer.Reset()

		select {
		case t.out <- env:
		case <-t.closed:
			return
		}
	}
}

// send marshals v to JSON and writes it as a single \n-terminated line.
// Concurrent calls are serialised. Returns an error if the transport is closed
// or the underlying writer fails.
func (t *transport) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	select {
	case <-t.closed:
		return errors.New("claude: transport closed")
	default:
	}

	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("claude: write: %w", err)
	}
	if _, err := t.stdin.Write([]byte("\n")); err != nil {
		return fmt.Errorf("claude: write: %w", err)
	}

	return nil
}

// closeStdin signals the CLI we're done sending input. The CLI then runs to
// completion and exits naturally; we wait for it in close().
func (t *transport) closeStdin() {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
}

// close terminates the subprocess and frees pipes. Safe to call multiple times.
// After close returns, stderrText() reflects the full stderr stream.
//
// Three-phase shutdown matching subprocess_cli.close() in the Python SDK:
// close stdin so the CLI flushes its session transcript and exits naturally;
// SIGTERM if it hasn't exited; SIGKILL as a last resort. readDone closes when
// stdout EOFs, which the OS does as soon as the process is gone — we use it
// as the "process exited" signal so we don't need a parallel cmd.Wait().
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

		// SIGTERM isn't supported on Windows (Process.Signal returns EWINDOWS
		// without touching the process); skip the cooperative wait in that
		// case and fall straight through to Kill.
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

// stderrText returns whatever stderr the subprocess produced so far.
func (t *transport) stderrText() string {
	return t.stderr.String()
}
