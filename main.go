package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/server"
)

func main() {
	portFlag := flag.Int("port", 8080, "server port")
	addressFlag := flag.String("address", "", "server address")
	configFlag := flag.String("config", "config.yaml", "configuration path")

	flag.Parse()

	cfg, err := config.Parse(*configFlag)

	if err != nil {
		panic(err)
	}

	cfg.Address = fmt.Sprintf("%s:%d", *addressFlag, *portFlag)

	s, err := server.New(cfg)

	if err != nil {
		cfg.Close()
		panic(err)
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := otel.Shutdown(ctx); err != nil {
			// The telemetry logger has already stopped.
			fmt.Fprintln(os.Stderr, "telemetry shutdown failed:", err)
		}
	}()

	if err := otel.Setup(); err != nil {
		cfg.Close()
		panic(err)
	}

	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Requests do not inherit the signal, so shutdown can let them finish.
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()

	httpServer := &http.Server{Addr: cfg.Address, Handler: s, BaseContext: func(net.Listener) context.Context { return base }}
	done := make(chan error, 1)
	go func() { done <- httpServer.ListenAndServe() }()

	select {
	case err := <-done:
		cfg.Close()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
		return
	case <-signals.Done():
	}

	// A second signal terminates immediately.
	stop()

	// The grace period is a ceiling, not a delay: an idle server stops immediately.
	// Realtime sockets have no natural end, so cancel them after HTTP drains.
	grace, cancelGrace := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelGrace()

	if err := httpServer.Shutdown(grace); err != nil {
		slog.Error("HTTP shutdown failed", "error", err)
		if err := httpServer.Close(); err != nil {
			slog.Error("HTTP close failed", "error", err)
		}
	}

	cancelBase()

	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cfg.Shutdown(deadline); err != nil {
		slog.Error("budget shutdown failed", "error", err)
	}
}
