package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"skaldi/internal/bootstrap"
	"skaldi/internal/discovery"
	"skaldi/internal/player"
	"skaldi/internal/resolver"
	"skaldi/internal/server"
	"skaldi/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := bootstrap.Run(logger); err != nil {
		logger.Error("Provisioning failed", "error", err)
		os.Exit(1)
	}

	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	mgr := player.NewManager(cfg, logger)
	res := resolver.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const port = 8080

	mdnsCleanup, mdnsActive := discovery.Register(ctx, logger, port)
	defer mdnsCleanup()

	playerDone := make(chan struct{})
	go func() {
		defer close(playerDone)
		if err := mgr.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("Player manager failed", "error", err)
			cancel()
		}
	}()

	srv := server.New(logger, mgr, res, web.IndexHTML, port)

	go func() {
		if err := srv.Start(mdnsActive); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed", "error", err)
			cancel()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
	case <-ctx.Done():
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	srv.Shutdown(shutdownCtx)
	mgr.Stop()

	select {
	case <-playerDone:
	case <-shutdownCtx.Done():
		cancel()
	}
}
