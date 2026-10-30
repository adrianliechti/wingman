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
	"github.com/adrianliechti/wingman/server"

	"github.com/adrianliechti/wingman/pkg/otel"
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

	// Fit the usual 30s termination grace period: in-flight HTTP requests get
	// up to 20s. Shutdown does not wait for hijacked realtime sockets, which
	// have no natural end; they are canceled with any remaining requests once
	// it returns. The budget reports of all operations are then drained.
	deadline, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	grace, cancelGrace := context.WithTimeout(deadline, 20*time.Second)
	defer cancelGrace()

	if err := httpServer.Shutdown(grace); err != nil {
		slog.Error("HTTP shutdown failed", "error", err)
	}

	cancelBase()

	if err := cfg.Shutdown(deadline); err != nil {
		slog.Error("budget shutdown failed", "error", err)
	}
}
