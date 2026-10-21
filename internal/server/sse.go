// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/reuski/skaldi/internal/player"
)

const clientBuffer = 10

type client struct {
	ch       chan []byte
	lastSnap player.Snapshot
}

type Broadcaster struct {
	clients   map[*client]struct{}
	clientsMu sync.Mutex
	updates   <-chan player.Snapshot
	latest    func() player.Snapshot
}

func NewBroadcaster(updates <-chan player.Snapshot, latest func() player.Snapshot) *Broadcaster {
	return &Broadcaster{
		clients: make(map[*client]struct{}),
		updates: updates,
		latest:  latest,
	}
}

func (b *Broadcaster) Run() {
	for snap := range b.updates {
		b.publish(snap)
	}
}

// Register and capture the initial snapshot atomically to avoid missed updates.
func (b *Broadcaster) Subscribe() chan []byte {
	ch := make(chan []byte, clientBuffer)

	b.clientsMu.Lock()
	defer b.clientsMu.Unlock()

	c := &client{ch: ch, lastSnap: b.latest()}
	b.clients[c] = struct{}{}

	if msg, err := encodeFrame(c.lastSnap); err == nil {
		ch <- msg
	}
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

func (b *Broadcaster) publish(snap player.Snapshot) {
	b.clientsMu.Lock()
	defer b.clientsMu.Unlock()

	for c := range b.clients {
		b.publishTo(c, snap)
	}
}

func (b *Broadcaster) publishTo(c *client, snap player.Snapshot) {
	// Queued snapshots must not roll back a newer subscription baseline.
	if snap.Version < c.lastSnap.Version {
		return
	}
	if player.SnapshotsEqual(c.lastSnap, snap) {
		return
	}

	msg, err := frame(c.lastSnap, snap)
	if err != nil {
		return
	}

	select {
	case c.ch <- msg:
		c.lastSnap = snap
	default:
		b.resync(c, snap)
	}
}

// Discarded deltas invalidate their baseline; reset with a full snapshot.
func (b *Broadcaster) resync(c *client, snap player.Snapshot) {
	msg, err := encodeFrame(snap)
	if err != nil {
		return
	}

	for {
		select {
		case <-c.ch:
			continue
		default:
		}
		break
	}

	select {
	case c.ch <- msg:
		c.lastSnap = snap
	default:
	}
}

func frame(previous, current player.Snapshot) ([]byte, error) {
	if delta := player.ComputeDelta(previous, current); delta != nil {
		return encodeFrame(delta)
	}
	return encodeFrame(current)
}

func encodeFrame(payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, "data: %s\n\n", data), nil
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

	if _, err := fmt.Fprintf(w, "retry: 3000\n"); err != nil {
		return
	}
	flusher.Flush()

	clientCh := s.broadcaster.Subscribe()
	defer s.broadcaster.RemoveClient(clientCh)

	notify := r.Context().Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-notify:
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case msg, ok := <-clientCh:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
