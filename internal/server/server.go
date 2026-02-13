// Package server provides HTTP handlers and SSE broadcasting for the jukebox API.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"skaldi/internal/player"
	"skaldi/internal/resolver"
)

type Server struct {
	server      *http.Server
	logger      *slog.Logger
	player      *player.Manager
	resolver    *resolver.Resolver
	indexHTML   []byte
	broadcaster *Broadcaster
}

func New(logger *slog.Logger, p *player.Manager, r *resolver.Resolver, indexHTML []byte, port int) *Server {
	mux := http.NewServeMux()

	s := &Server{
		logger:      logger,
		player:      p,
		resolver:    r,
		indexHTML:   indexHTML,
		broadcaster: NewBroadcaster(p.StateUpdates),
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}

	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /suggest", s.handleSuggest)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("POST /queue", s.handleQueue)
	mux.HandleFunc("POST /playback", s.handlePlayback)
	mux.HandleFunc("DELETE /queue/{index}", s.handleRemove)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("POST /upload", s.handleUpload)

	s.server.Handler = mux

	return s
}

func (s *Server) Start(mdnsActive bool) error {
	go s.broadcaster.Run()

	s.printReadyMessage(mdnsActive)

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) printReadyMessage(mdnsActive bool) {
	port := s.server.Addr

	if mdnsActive {
		s.logger.Info(fmt.Sprintf("http://skaldi.local%s", port))
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}

	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}

			msg := fmt.Sprintf("http://%s%s", ip.String(), port)
			if mdnsActive {
				s.logger.Debug("Also available at", "url", msg)
			} else {
				s.logger.Info(fmt.Sprintf("Skaldi ready at %s", msg))
				return
			}
		}
	}
}
