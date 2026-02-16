// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"skaldi/internal/player"
)

type client struct {
	ch       chan []byte
	lastSnap player.Snapshot
}

type Broadcaster struct {
	clients   map[*client]struct{}
	clientsMu sync.Mutex
	updates   <-chan player.Snapshot
	lastSnap  player.Snapshot
}

func NewBroadcaster(updates <-chan player.Snapshot) *Broadcaster {
	return &Broadcaster{
		clients: make(map[*client]struct{}),
		updates: updates,
	}
}

func (b *Broadcaster) Run() {
	for snap := range b.updates {
		b.clientsMu.Lock()
		b.lastSnap = snap

		for c := range b.clients {
			var payload []byte
			var err error

			if delta := player.ComputeDelta(c.lastSnap, snap); delta != nil {
				payload, err = json.Marshal(delta)
			} else {
				payload, err = json.Marshal(snap)
			}

			if err != nil {
				continue
			}

			msg := fmt.Appendf(nil, "data: %s\n\n", payload)
			select {
			case c.ch <- msg:
				c.lastSnap = snap
			default:
			}
		}
		b.clientsMu.Unlock()
	}
}

func (b *Broadcaster) AddClient(initialSnap player.Snapshot) chan []byte {
	ch := make(chan []byte, 10)
	b.clientsMu.Lock()
	c := &client{ch: ch, lastSnap: initialSnap}
	b.clients[c] = struct{}{}
	b.clientsMu.Unlock()
	return ch
}

func (b *Broadcaster) RemoveClient(ch chan []byte) {
	b.clientsMu.Lock()
	for c := range b.clients {
		if c.ch == ch {
			delete(b.clients, c)
			close(c.ch)
			break
		}
	}
	b.clientsMu.Unlock()
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	fmt.Fprintf(w, "retry: 3000\n")
	initialSnap := s.player.State.Snapshot()
	data, _ := json.Marshal(initialSnap)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	clientCh := s.broadcaster.AddClient(initialSnap)
	defer s.broadcaster.RemoveClient(clientCh)

	notify := r.Context().Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-notify:
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case msg, ok := <-clientCh:
			if !ok {
				return
			}
			_, _ = w.Write(msg)
			flusher.Flush()
		}
	}
}
