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
	defer func() {
		if err := cfg.Close(); err != nil {
			slog.Error("budget shutdown failed", "error", err)
		}
	}()

	cfg.Address = fmt.Sprintf("%s:%d", *addressFlag, *portFlag)

	s, err := server.New(cfg)

	if err != nil {
		panic(err)
	}

	if err := otel.Setup(); err != nil {
		panic(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpServer := &http.Server{Addr: cfg.Address, Handler: s, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- httpServer.ListenAndServe() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdown); err != nil {
			slog.Error("HTTP shutdown failed", "error", err)
		}
	}
}
