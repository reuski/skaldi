// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/reuski/skaldi/internal/player"
)

func TestNewBroadcaster(t *testing.T) {
	updates := make(chan player.Snapshot)
	b := NewBroadcaster(updates)

	if b == nil {
		t.Fatal("NewBroadcaster() returned nil")
	}

	if b.clients == nil {
		t.Error("clients map not initialized")
	}

	if b.updates != updates {
		t.Error("updates channel not set correctly")
	}
}

func TestBroadcaster_AddClient(t *testing.T) {
	updates := make(chan player.Snapshot)
	b := NewBroadcaster(updates)

	ch := b.AddClient(player.Snapshot{})

	if ch == nil {
		t.Fatal("AddClient() returned nil channel")
	}

	b.clientsMu.Lock()
	found := false
	for c := range b.clients {
		if c.ch == ch {
			found = true
			break
		}
	}
	b.clientsMu.Unlock()

	if !found {
		t.Error("Client channel not added to clients map")
	}

	b.RemoveClient(ch)
}

func TestBroadcaster_RemoveClient(t *testing.T) {
	updates := make(chan player.Snapshot)
	b := NewBroadcaster(updates)

	ch := b.AddClient(player.Snapshot{})
	b.RemoveClient(ch)

	b.clientsMu.Lock()
	found := false
	for c := range b.clients {
		if c.ch == ch {
			found = true
			break
		}
	}
	b.clientsMu.Unlock()

	if found {
		t.Error("Client channel should have been removed from clients map")
	}

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Channel should be closed after removal")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Channel should be closed immediately")
	}
}

func TestBroadcaster_Run(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	b := NewBroadcaster(updates)

	go b.Run()

	clientCh := b.AddClient(player.Snapshot{})
	defer b.RemoveClient(clientCh)

	snapshot := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 60.0,
		Duration:    180.0,
		Queue: []player.QueueItem{
			{Index: 0, Title: "Test Track"},
		},
	}

	updates <- snapshot

	select {
	case msg := <-clientCh:
		if len(msg) == 0 {
			t.Error("Received empty message")
		}

		prefix := []byte("data: ")
		if len(msg) < len(prefix) || string(msg[:len(prefix)]) != "data: " {
			t.Errorf("Message should start with 'data: ', got %q", string(msg))
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Timeout waiting for message")
	}
}

func TestBroadcaster_MultipleClients(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	b := NewBroadcaster(updates)

	go b.Run()

	client1 := b.AddClient(player.Snapshot{})
	client2 := b.AddClient(player.Snapshot{})
	defer b.RemoveClient(client1)
	defer b.RemoveClient(client2)

	snapshot := player.Snapshot{
		Version: 1,
		Status:  player.StatusPlaying,
	}

	updates <- snapshot

	for i, ch := range []chan []byte{client1, client2} {
		select {
		case msg := <-ch:
			if len(msg) == 0 {
				t.Errorf("Client %d received empty message", i+1)
			}
		case <-time.After(500 * time.Millisecond):
			t.Errorf("Timeout waiting for message on client %d", i+1)
		}
	}
}

func TestBroadcaster_DeltaSending(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	b := NewBroadcaster(updates)

	go b.Run()

	clientCh := b.AddClient(player.Snapshot{})
	defer b.RemoveClient(clientCh)

	snapshot1 := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 60.0,
		Duration:    180.0,
	}

	updates <- snapshot1

	select {
	case msg := <-clientCh:
		if len(msg) == 0 {
			t.Error("Received empty message")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Timeout waiting for first message")
	}

	snapshot2 := player.Snapshot{
		Version:     2,
		Status:      player.StatusPlaying,
		CurrentTime: 65.0,
		Duration:    180.0,
	}

	updates <- snapshot2

	select {
	case msg := <-clientCh:
		msgStr := string(msg)
		if len(msgStr) == 0 {
			t.Error("Received empty delta message")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Timeout waiting for delta message")
	}
}

func TestBroadcaster_ClientBuffer(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	b := NewBroadcaster(updates)

	go b.Run()

	clientCh := b.AddClient(player.Snapshot{})
	defer b.RemoveClient(clientCh)

	for i := range 15 {
		updates <- player.Snapshot{Version: uint64(i + 1), Status: player.StatusPlaying}
	}

	time.Sleep(100 * time.Millisecond)

	received := 0
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-clientCh:
				received++
			case <-time.After(100 * time.Millisecond):
				done <- true
				return
			}
		}
	}()

	<-done

	if received == 0 {
		t.Error("Should have received some messages")
	}
}

func TestBroadcaster_ConcurrentAccess(t *testing.T) {
	updates := make(chan player.Snapshot, 100)
	b := NewBroadcaster(updates)

	go b.Run()

	for range 10 {
		go func() {
			ch := b.AddClient(player.Snapshot{})
			time.Sleep(50 * time.Millisecond)
			b.RemoveClient(ch)
		}()
	}

	for i := range 20 {
		updates <- player.Snapshot{Version: uint64(i + 1), Status: player.StatusPlaying}
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(200 * time.Millisecond)

	b.clientsMu.Lock()
	clientCount := len(b.clients)
	b.clientsMu.Unlock()

	if clientCount != 0 {
		t.Errorf("Expected 0 clients, got %d", clientCount)
	}
}

func TestSSEMessageFormat(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	b := NewBroadcaster(updates)

	go b.Run()

	clientCh := b.AddClient(player.Snapshot{})
	defer b.RemoveClient(clientCh)

	snapshot := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 42.0,
		Duration:    180.0,
	}

	updates <- snapshot

	select {
	case msg := <-clientCh:
		msgStr := string(msg)

		expectedPrefix := "data: "
		if len(msgStr) < len(expectedPrefix) || msgStr[:len(expectedPrefix)] != expectedPrefix {
			t.Errorf("Message should start with 'data: ', got: %s", msgStr)
		}

		if msgStr[len(msgStr)-2:] != "\n\n" {
			t.Errorf("Message should end with \\n\\n, got: %q", msgStr[len(msgStr)-2:])
		}

		jsonStart := len(expectedPrefix)
		jsonEnd := len(msgStr) - 2
		jsonData := msgStr[jsonStart:jsonEnd]

		var parsed player.Snapshot
		if err := json.Unmarshal([]byte(jsonData), &parsed); err != nil {
			t.Errorf("Failed to parse JSON data: %v", err)
		}

		if parsed.Status != snapshot.Status {
			t.Errorf("Status mismatch: got %v, want %v", parsed.Status, snapshot.Status)
		}

		if parsed.CurrentTime != snapshot.CurrentTime {
			t.Errorf("CurrentTime mismatch: got %f, want %f", parsed.CurrentTime, snapshot.CurrentTime)
		}

	case <-time.After(500 * time.Millisecond):
		t.Error("Timeout waiting for message")
	}
}
