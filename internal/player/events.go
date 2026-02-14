package player

import (
	"context"
	"encoding/json"
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
	properties := []string{"idle-active", "pause", "time-pos", "duration", "playlist", "media-title", "playlist-pos"}
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
		if val, ok := e.Data.(bool); ok {
			m.State.SetIdle(val)
			if val {
				m.State.SetTimePos(0)
				m.State.SetDuration(0)
			}
			shouldBroadcast = true
		}
	case "pause":
		if val, ok := e.Data.(bool); ok {
			m.State.SetPaused(val)
			shouldBroadcast = true
		}
	case "time-pos":
		if val, ok := e.Data.(float64); ok {
			m.State.SetTimePos(val)
			shouldBroadcast = true
		}
	case "duration":
		if val, ok := e.Data.(float64); ok {
			m.State.SetDuration(val)
			shouldBroadcast = true
		}
	case "playlist":
		data, err := json.Marshal(e.Data)
		if err == nil {
			var entries []MpvPlaylistEntry
			if err := json.Unmarshal(data, &entries); err == nil {
				m.State.SetPlaylist(entries)
				m.State.PruneMetadata()
				shouldBroadcast = true
				m.checkTempFiles(entries)
			}
		}
	case "playlist-pos":
		if val, ok := e.Data.(float64); ok {
			m.State.SetPlaylistPos(int(val))
			shouldBroadcast = true
		} else {
			m.State.SetPlaylistPos(-1)
			shouldBroadcast = true
		}
	}

	if shouldBroadcast {
		select {
		case m.StateUpdates <- m.State.Snapshot():
		default:
		}
	}
}
