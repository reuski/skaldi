// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"context"
	"encoding/json"
	"time"

	"github.com/reuski/skaldi/internal/history"
)

// Stop joins this loop before closing state publication.
func (m *Manager) StartEventLoop(ctx context.Context) {
	m.startLoop(ctx, func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			case event := <-m.ipc.Events:
				m.handleEvent(event)
			}
		}
	})
}

func (m *Manager) RegisterObservers() {
	properties := []string{
		"idle-active",
		"pause",
		"time-pos",
		"duration",
		"ao-volume",
		"ao-mute",
		"volume",
		"mute",
		"playlist",
		"media-title",
		"playlist-pos",
	}
	for _, prop := range properties {
		go func(p string) {
			_, _ = m.ipc.Exec("observe_property", 0, p)
		}(prop)
	}
}

func (m *Manager) handleEvent(e Event) {
	switch e.Event {
	case "end-file":
		if m.handleEndFile(e) {
			m.broadcastSnapshot()
		}
		return
	case "property-change":
	default:
		return
	}

	shouldBroadcast := false

	switch e.Name {
	case "idle-active":
		shouldBroadcast = m.handleIdleActive(e.Data)
	case "pause":
		shouldBroadcast = m.handlePause(e.Data)
	case "time-pos":
		shouldBroadcast = m.handleTimePos(e.Data)
	case "duration":
		shouldBroadcast = m.handleDuration(e.Data)
	case "ao-volume":
		shouldBroadcast = m.handleAOVolume(e.Data)
	case "ao-mute":
		shouldBroadcast = m.handleAOMute(e.Data)
	case "volume":
		shouldBroadcast = m.handleVolume(e.Data)
	case "mute":
		shouldBroadcast = m.handleMute(e.Data)
	case "playlist":
		shouldBroadcast = m.handlePlaylist(e.Data)
	case "playlist-pos":
		shouldBroadcast = m.handlePlaylistPos(e.Data)
	}

	if shouldBroadcast {
		m.broadcastSnapshot()
	}
}

// Serialize capture and publication so older snapshots cannot overwrite newer ones.
func (m *Manager) broadcastSnapshot() {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	if m.stateUpdatesOff {
		return
	}
	m.publishSnapshotLocked(m.State.Snapshot())
}

// Replace stale full snapshots under pressure so clients receive terminal state.
func (m *Manager) publishSnapshotLocked(s Snapshot) {
	for {
		select {
		case m.StateUpdates <- s:
			return
		default:
		}

		select {
		case <-m.StateUpdates:
			continue
		default:
		}

		if cap(m.StateUpdates) == 0 {
			return
		}

		// A concurrent consumer emptied the buffer; retry while publishMu excludes producers.
	}
}

func (m *Manager) closeStateUpdates() {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	if m.stateUpdatesOff {
		return
	}
	m.stateUpdatesOff = true
	close(m.StateUpdates)
}

func (m *Manager) handleEndFile(e Event) bool {
	if e.Reason != "error" {
		return false
	}
	m.logger.Debug("mpv reported a playback error", "entry_id", e.PlaylistEntryID)
	m.State.RecordPlaybackFailure(e.PlaylistEntryID)
	return true
}

func (m *Manager) handleIdleActive(data interface{}) bool {
	if val, ok := data.(bool); ok {
		m.State.SetIdle(val)
		if val {
			m.State.SetTimePos(0)
			m.State.SetDuration(0)
		}
		return true
	}
	return false
}

func (m *Manager) handlePause(data interface{}) bool {
	if val, ok := data.(bool); ok {
		m.State.SetPaused(val)
		return true
	}
	return false
}

func (m *Manager) handleTimePos(data interface{}) bool {
	if val, ok := data.(float64); ok {
		m.State.SetTimePos(val)
		return true
	}
	return false
}

func (m *Manager) handleDuration(data interface{}) bool {
	if val, ok := data.(float64); ok {
		m.State.SetDuration(val)
		return true
	}
	return false
}

func (m *Manager) handleAOVolume(data interface{}) bool {
	val, ok := data.(float64)
	if !ok {
		return false
	}

	if m.preferAOVolume.CompareAndSwap(false, true) {
		go m.activateAOVolume()
		return false
	}

	m.State.SetVolume(val)
	return true
}

func (m *Manager) handleAOMute(data interface{}) bool {
	val, ok := data.(bool)
	if !ok {
		return false
	}

	if m.preferAOMute.CompareAndSwap(false, true) {
		go m.activateAOMute()
		return false
	}

	m.State.SetMuted(val)
	return true
}

func (m *Manager) handleVolume(data interface{}) bool {
	if m.preferAOVolume.Load() {
		return false
	}
	if val, ok := data.(float64); ok {
		m.State.SetVolume(val)
		return true
	}
	return false
}

func (m *Manager) handleMute(data interface{}) bool {
	if m.preferAOMute.Load() {
		return false
	}
	if val, ok := data.(bool); ok {
		m.State.SetMuted(val)
		return true
	}
	return false
}

func (m *Manager) handlePlaylist(data interface{}) bool {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return false
	}
	var entries []MpvPlaylistEntry
	if err := json.Unmarshal(dataBytes, &entries); err != nil {
		return false
	}
	m.State.SetPlaylist(entries)
	m.State.PruneMetadata()
	m.checkTempFiles(entries)
	return true
}

func (m *Manager) handlePlaylistPos(data interface{}) bool {
	idx := -1
	if val, ok := data.(float64); ok {
		idx = int(val)
	}

	item := m.State.SetPlaylistPos(idx)
	if item == nil {
		return idx >= 0
	}

	if idx < 0 {
		return true
	}

	entry := history.Entry{
		Time:     time.Now(),
		Title:    item.Title,
		Duration: item.Duration,
	}
	if item.Metadata != nil {
		entry.Artist = item.Metadata.Artist
		entry.URL = item.Metadata.WebpageURL
		if entry.URL == "" {
			entry.URL = item.Metadata.URL
		}
	}
	if entry.Title == "" {
		entry.Title = item.Filename
	}
	if entry.Title != "" || entry.URL != "" {
		m.history.Log(entry)
	}
	return true
}
