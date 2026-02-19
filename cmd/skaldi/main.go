// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/reuski/skaldi/internal/bootstrap"
	"github.com/reuski/skaldi/internal/discovery"
	"github.com/reuski/skaldi/internal/player"
	"github.com/reuski/skaldi/internal/resolver"
	"github.com/reuski/skaldi/internal/server"
	"github.com/reuski/skaldi/web"
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

	logger.Info("Bye")
	signal.Stop(sigCh)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = srv.Shutdown(shutdownCtx)
	mgr.Stop()
	cancel()

	select {
	case <-playerDone:
	case <-shutdownCtx.Done():
	}
}
