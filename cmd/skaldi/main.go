// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
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
	"github.com/reuski/skaldi/internal/update"
	"github.com/reuski/skaldi/web"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if len(os.Args) > 1 {
		if code, handled := runCommand(logger, os.Args[1]); handled {
			os.Exit(code)
		}
	}

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
	res, err := resolver.New(cfg)
	if err != nil {
		logger.Error("Failed to initialize resolver", "error", err)
		os.Exit(1)
	}
	for _, warning := range res.Warnings() {
		logger.Warn("Optional resolver source disabled", "error", warning)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := cfg.Port

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

	srv := server.New(logger, mgr, res, web.IndexHTML, port, version)

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

func runCommand(logger *slog.Logger, cmd string) (int, bool) {
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println(version)
		return 0, true
	case "update":
		if err := update.Run(version, logger); err != nil {
			logger.Error("Update failed", "error", err)
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}
