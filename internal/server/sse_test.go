// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reuski/skaldi/internal/player"
)

type snapshotSource struct {
	mu   sync.Mutex
	snap player.Snapshot
}

func newSnapshotSource(snap player.Snapshot) *snapshotSource {
	return &snapshotSource{snap: snap}
}

func (s *snapshotSource) set(snap player.Snapshot) {
	s.mu.Lock()
	s.snap = snap
	s.mu.Unlock()
}

func (s *snapshotSource) get() player.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

type broadcastFrame struct {
	Version     uint64                  `json:"v"`
	Status      player.PlaybackStatus   `json:"status"`
	CurrentTime float64                 `json:"current_time"`
	Duration    float64                 `json:"duration"`
	Queue       json.RawMessage         `json:"queue"`
	Failure     *player.PlaybackFailure `json:"last_playback_failure"`
}

func decodeFrame(t *testing.T, msg []byte) broadcastFrame {
	t.Helper()

	payload := strings.TrimSuffix(strings.TrimPrefix(string(msg), "data: "), "\n\n")

	var decoded broadcastFrame
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v (%q)", err, payload)
	}
	return decoded
}

func isFullFrame(f broadcastFrame) bool {
	return len(f.Queue) > 0
}

func assertFullFrame(t *testing.T, msg []byte) broadcastFrame {
	t.Helper()

	frame := decodeFrame(t, msg)
	if !isFullFrame(frame) {
		t.Fatalf("want a full snapshot (queue present), got a delta: %q", msg)
	}
	return frame
}

func subscribeClient(t *testing.T, b *Broadcaster) chan []byte {
	t.Helper()

	ch := b.Subscribe()
	t.Cleanup(func() { b.RemoveClient(ch) })

	select {
	case msg := <-ch:
		assertFullFrame(t, msg)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the initial full snapshot")
	}
	return ch
}

func readFrame(t *testing.T, ch chan []byte) []byte {
	t.Helper()

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("client channel closed")
		}
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a frame")
		return nil
	}
}

func assertNoFrame(t *testing.T, ch chan []byte) {
	t.Helper()

	select {
	case msg := <-ch:
		t.Fatalf("unexpected frame: %q", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNewBroadcaster(t *testing.T) {
	updates := make(chan player.Snapshot)
	latest := func() player.Snapshot { return player.Snapshot{} }
	b := NewBroadcaster(updates, latest)

	if b == nil {
		t.Fatal("NewBroadcaster() returned nil")
	} else if b.clients == nil {
		t.Error("clients map not initialized")
	} else if b.updates != updates {
		t.Error("updates channel not set correctly")
	} else if b.latest == nil {
		t.Error("snapshot source not set correctly")
	}
}

func TestBroadcaster_Subscribe(t *testing.T) {
	updates := make(chan player.Snapshot)
	b := NewBroadcaster(updates, func() player.Snapshot { return player.Snapshot{} })

	ch := b.Subscribe()

	if ch == nil {
		t.Fatal("Subscribe() returned nil channel")
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

	assertFullFrame(t, readFrame(t, ch))

	b.RemoveClient(ch)
}

func TestBroadcaster_RemoveClient(t *testing.T) {
	updates := make(chan player.Snapshot)
	b := NewBroadcaster(updates, func() player.Snapshot { return player.Snapshot{} })

	ch := subscribeClient(t, b)
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
	source := newSnapshotSource(player.Snapshot{})
	b := NewBroadcaster(updates, source.get)

	go b.Run()
	defer close(updates)

	clientCh := subscribeClient(t, b)

	snapshot := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 60.0,
		Duration:    180.0,
		Queue: []player.QueueItem{
			{Index: 0, Title: "Test Track"},
		},
	}
	source.set(snapshot)

	updates <- snapshot

	msg := readFrame(t, clientCh)
	if len(msg) == 0 {
		t.Error("Received empty message")
	}

	prefix := []byte("data: ")
	if len(msg) < len(prefix) || string(msg[:len(prefix)]) != "data: " {
		t.Errorf("Message should start with 'data: ', got %q", string(msg))
	}
}

func TestBroadcaster_MultipleClients(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	source := newSnapshotSource(player.Snapshot{})
	b := NewBroadcaster(updates, source.get)

	go b.Run()
	defer close(updates)

	client1 := subscribeClient(t, b)
	client2 := subscribeClient(t, b)

	snapshot := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 15.0,
		Queue:       []player.QueueItem{},
	}
	source.set(snapshot)

	updates <- snapshot

	for i, ch := range []chan []byte{client1, client2} {
		frame := assertFullFrame(t, readFrame(t, ch))
		if frame.CurrentTime != 15.0 {
			t.Errorf("Client %d current_time = %v, want 15", i+1, frame.CurrentTime)
		}
	}
}

func TestBroadcaster_DeltaSending(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	source := newSnapshotSource(player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 60.0,
		Duration:    180.0,
	})
	b := NewBroadcaster(updates, source.get)

	go b.Run()
	defer close(updates)

	clientCh := subscribeClient(t, b)

	snapshot := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 65.0,
		Duration:    180.0,
	}
	source.set(snapshot)

	updates <- snapshot

	frame := decodeFrame(t, readFrame(t, clientCh))
	if frame.CurrentTime != 65.0 {
		t.Fatalf("current_time = %v, want the delta value 65", frame.CurrentTime)
	}
	if isFullFrame(frame) {
		t.Fatalf("want a delta for an unchanged queue, got a full snapshot: %v", frame)
	}
}

func TestBroadcaster_ClientBuffer(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	source := newSnapshotSource(player.Snapshot{})
	b := NewBroadcaster(updates, source.get)

	go b.Run()
	defer close(updates)

	clientCh := subscribeClient(t, b)

	for i := range 15 {
		snapshot := player.Snapshot{Version: uint64(i + 1), Status: player.StatusPlaying, Queue: []player.QueueItem{}}
		source.set(snapshot)
		updates <- snapshot
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
	source := newSnapshotSource(player.Snapshot{})
	b := NewBroadcaster(updates, source.get)

	go b.Run()
	defer close(updates)

	for range 10 {
		go func() {
			ch := b.Subscribe()
			time.Sleep(50 * time.Millisecond)
			b.RemoveClient(ch)
		}()
	}

	for i := range 20 {
		snapshot := player.Snapshot{Version: uint64(i + 1), Status: player.StatusPlaying, Queue: []player.QueueItem{}}
		source.set(snapshot)
		updates <- snapshot
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

func TestBroadcaster_SkipsUnchangedSnapshots(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	source := newSnapshotSource(player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 42.0,
		Duration:    180.0,
	})
	b := NewBroadcaster(updates, source.get)

	go b.Run()
	defer close(updates)

	clientCh := subscribeClient(t, b)

	snapshot := player.Snapshot{
		Version:     2,
		Status:      player.StatusPlaying,
		CurrentTime: 42.0,
		Duration:    180.0,
	}
	source.set(snapshot)

	updates <- snapshot

	assertNoFrame(t, clientCh)
}

func TestSSEMessageFormat(t *testing.T) {
	updates := make(chan player.Snapshot, 10)
	source := newSnapshotSource(player.Snapshot{})
	b := NewBroadcaster(updates, source.get)

	go b.Run()
	defer close(updates)

	clientCh := subscribeClient(t, b)

	snapshot := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 42.0,
		Duration:    180.0,
	}
	source.set(snapshot)

	updates <- snapshot

	msgStr := string(readFrame(t, clientCh))

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
}

func TestSSEDeliversPlaybackFailure(t *testing.T) {
	s, _ := setupTestServer(t)

	updates := make(chan player.Snapshot, 4)
	b := NewBroadcaster(updates, s.player.State.Snapshot)
	go b.Run()
	defer close(updates)

	clientCh := subscribeClient(t, b)

	s.player.State.RecordPlaybackFailure(4)
	updates <- s.player.State.Snapshot()

	frame := assertFullFrame(t, readFrame(t, clientCh))
	if frame.Failure == nil || frame.Failure.EntryID != 4 {
		t.Fatalf("frame missing the recorded failure: %+v", frame)
	}
}

func TestBroadcasterResyncsSaturatedClientWithLatestState(t *testing.T) {
	base := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 10,
		Duration:    100,
		Queue:       []player.QueueItem{},
	}

	updates := make(chan player.Snapshot)
	source := newSnapshotSource(base)
	b := NewBroadcaster(updates, source.get)

	// Publish synchronously to control buffer pressure.
	clientCh := subscribeClient(t, b)

	for i := 0; i < clientBuffer; i++ {
		snapshot := player.Snapshot{
			Version:     1,
			Status:      player.StatusPlaying,
			CurrentTime: float64(11 + i),
			Duration:    100,
			Queue:       []player.QueueItem{},
		}
		source.set(snapshot)
		b.publish(snapshot)
	}
	if got := len(clientCh); got != clientBuffer {
		t.Fatalf("queued frames = %d, want the client buffer full at %d", got, clientBuffer)
	}

	terminal := player.Snapshot{
		Version:     2,
		Status:      player.StatusIdle,
		Queue:       []player.QueueItem{},
		LastFailure: &player.PlaybackFailure{Occurrence: 1, EntryID: 7, Title: "Broken"},
	}
	source.set(terminal)
	b.publish(terminal)

	if got := len(clientCh); got != 1 {
		t.Fatalf("queued frames = %d, want the stale backlog replaced by one snapshot", got)
	}

	sentinel := player.Snapshot{
		Version:     3,
		Status:      player.StatusIdle,
		CurrentTime: 4242,
		Queue:       []player.QueueItem{},
		LastFailure: terminal.LastFailure,
	}
	source.set(sentinel)
	b.publish(sentinel)

	reset := assertFullFrame(t, readFrame(t, clientCh))
	if reset.Version != 2 || reset.Failure == nil || reset.Failure.EntryID != 7 {
		t.Fatalf("reset frame = %+v, want the terminal snapshot with its failure", reset)
	}

	next := decodeFrame(t, readFrame(t, clientCh))
	if next.Version != 3 || next.CurrentTime != 4242 {
		t.Fatalf("frame after the reset = %+v, want the sentinel update", next)
	}
	if isFullFrame(next) {
		t.Fatalf("want a delta after the reset baseline, got a full snapshot: %+v", next)
	}

	assertNoFrame(t, clientCh)
}

func TestSubscribeRejectsQueuedSnapshotOlderThanBaseline(t *testing.T) {
	updates := make(chan player.Snapshot, 8)
	source := newSnapshotSource(player.Snapshot{
		Version:     7,
		Status:      player.StatusPlaying,
		CurrentTime: 30,
		Duration:    100,
		Queue:       []player.QueueItem{},
	})
	b := NewBroadcaster(updates, source.get)

	updates <- player.Snapshot{
		Version:     5,
		Status:      player.StatusPlaying,
		CurrentTime: 12,
		Duration:    100,
		Queue:       []player.QueueItem{},
	}

	clientCh := subscribeClient(t, b)

	go b.Run()
	defer close(updates)

	newer := player.Snapshot{
		Version:     8,
		Status:      player.StatusPlaying,
		CurrentTime: 31,
		Duration:    100,
		Queue:       []player.QueueItem{},
	}
	source.set(newer)
	updates <- newer

	frame := decodeFrame(t, readFrame(t, clientCh))
	if frame.Version != 8 || frame.CurrentTime != 31 {
		t.Fatalf("frame = %+v, want the version 8 update rather than the queued version 5 one", frame)
	}
	if isFullFrame(frame) {
		t.Fatalf("want a delta against the subscription baseline, got a full snapshot: %+v", frame)
	}

	assertNoFrame(t, clientCh)
}

func TestBroadcasterKeepsClientsIndependentUnderPressure(t *testing.T) {
	base := player.Snapshot{
		Version:     1,
		Status:      player.StatusPlaying,
		CurrentTime: 1,
		Duration:    100,
		Queue:       []player.QueueItem{},
	}

	updates := make(chan player.Snapshot)
	source := newSnapshotSource(base)
	b := NewBroadcaster(updates, source.get)

	fast := subscribeClient(t, b)
	slow := subscribeClient(t, b)

	// Publish synchronously to control buffer pressure.
	for i := 2; i <= clientBuffer+6; i++ {
		snapshot := player.Snapshot{
			Version:     1,
			Status:      player.StatusPlaying,
			CurrentTime: float64(i),
			Duration:    100,
			Queue:       []player.QueueItem{},
		}
		b.publish(snapshot)

		frame := decodeFrame(t, readFrame(t, fast))
		if frame.CurrentTime != float64(i) {
			t.Fatalf("fast client current_time = %v, want %d", frame.CurrentTime, i)
		}
	}

	var queued [][]byte
	for {
		select {
		case msg := <-slow:
			queued = append(queued, msg)
			continue
		default:
		}
		break
	}

	if len(queued) == 0 || len(queued) > clientBuffer {
		t.Fatalf("stalled client queued %d frames, want 1..%d", len(queued), clientBuffer)
	}
	if !isFullFrame(decodeFrame(t, queued[0])) {
		t.Fatalf("stalled client's oldest frame is a delta, want the reset snapshot: %q", queued[0])
	}
	for i, msg := range queued[1:] {
		if isFullFrame(decodeFrame(t, msg)) {
			t.Fatalf("frame %d after the reset is a full snapshot, want a delta: %q", i+1, msg)
		}
	}

	newest := decodeFrame(t, queued[len(queued)-1])
	if newest.CurrentTime != float64(clientBuffer+6) {
		t.Fatalf("stalled client current_time = %v, want the latest %d", newest.CurrentTime, clientBuffer+6)
	}
}
