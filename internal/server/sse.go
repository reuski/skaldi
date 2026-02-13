package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"skaldi/internal/player"
)

type Broadcaster struct {
	clients   map[chan []byte]bool
	clientsMu sync.Mutex
	updates   <-chan player.Snapshot
	lastState []byte
}

func NewBroadcaster(updates <-chan player.Snapshot) *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan []byte]bool),
		updates: updates,
	}
}

func (b *Broadcaster) Run() {
	for snapshot := range b.updates {
		data, err := json.Marshal(snapshot)
		if err != nil {
			continue
		}

		msg := []byte(fmt.Sprintf("data: %s\n\n", data))

		b.clientsMu.Lock()
		b.lastState = msg
		for client := range b.clients {
			select {
			case client <- msg:
			default:
			}
		}
		b.clientsMu.Unlock()
	}
}

func (b *Broadcaster) AddClient() chan []byte {
	ch := make(chan []byte, 10)
	b.clientsMu.Lock()
	b.clients[ch] = true
	b.clientsMu.Unlock()
	return ch
}

func (b *Broadcaster) RemoveClient(ch chan []byte) {
	b.clientsMu.Lock()
	delete(b.clients, ch)
	close(ch)
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

	clientCh := s.broadcaster.AddClient()
	defer s.broadcaster.RemoveClient(clientCh)

	currentSnapshot := s.player.State.Snapshot()
	data, _ := json.Marshal(currentSnapshot)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	notify := r.Context().Done()

	for {
		select {
		case <-notify:
			return
		case msg, ok := <-clientCh:
			if !ok {
				return
			}
			w.Write(msg)
			flusher.Flush()
		}
	}
}
