// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"context"
	"encoding/json"
	"time"

	"github.com/reuski/skaldi/internal/history"
)

func (m *Manager) StartEventLoop(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-m.ipc.Events:
				m.handleEvent(event)
			}
		}
	}()
}

func (m *Manager) RegisterObservers() {
	properties := []string{
		"idle-active",
		"pause",
		"time-pos",
		"duration",
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
	if e.Event != "property-change" {
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
		select {
		case m.StateUpdates <- m.State.Snapshot():
		default:
		}
	}
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

func (m *Manager) handleVolume(data interface{}) bool {
	if val, ok := data.(float64); ok {
		m.State.SetVolume(val)
		return true
	}
	return false
}

func (m *Manager) handleMute(data interface{}) bool {
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
	if val, ok := data.(float64); ok {
		idx := int(val)
		m.State.SetPlaylistPos(idx)
		if idx >= 0 {
			m.State.mu.RLock()
			if idx < len(m.State.playlist) {
				entry := m.State.playlist[idx]
				histEntry := history.Entry{
					Timestamp: time.Now(),
				}
				if track, ok := m.State.metadata[entry.Filename]; ok {
					histEntry.Title = track.Title
					histEntry.Artist = track.Artist
					histEntry.SourceURL = track.WebpageURL
					if histEntry.SourceURL == "" {
						histEntry.SourceURL = track.URL
					}
				} else {
					if histEntry.Title == "" {
						histEntry.Title = entry.Filename
					}
				}
				if histEntry.Title != "" || histEntry.SourceURL != "" {
					m.history.Log(histEntry)
				}
			}
			m.State.mu.RUnlock()
		}
		return true
	}
	m.State.SetPlaylistPos(-1)
	return true
}
