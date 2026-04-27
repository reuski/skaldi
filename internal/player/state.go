// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"slices"
	"sync"
	"time"

	"github.com/reuski/skaldi/internal/resolver"
)

type PlaybackStatus string

const (
	StatusIdle    PlaybackStatus = "idle"
	StatusPlaying PlaybackStatus = "playing"
	StatusPaused  PlaybackStatus = "paused"

	maxRecentPlayed = 3
)

type QueueItem struct {
	ID       int             `json:"id,omitempty"`
	Index    int             `json:"index"`
	Filename string          `json:"filename"`
	Title    string          `json:"title,omitempty"`
	Duration float64         `json:"duration,omitempty"`
	Metadata *resolver.Track `json:"metadata,omitempty"`
}

type Snapshot struct {
	Version     uint64         `json:"v"`
	Status      PlaybackStatus `json:"status"`
	CurrentTime float64        `json:"current_time"`
	Duration    float64        `json:"duration"`
	Volume      float64        `json:"volume"`
	Muted       bool           `json:"muted"`
	Queue       []QueueItem    `json:"queue"`
	History     []QueueItem    `json:"history"`
	Upcoming    []QueueItem    `json:"upcoming"`
	CurrentIdx  int            `json:"current_index"`
	NowPlaying  *QueueItem     `json:"now_playing,omitempty"`
}

type Delta struct {
	Version     uint64          `json:"v"`
	CurrentTime *float64        `json:"current_time,omitempty"`
	Duration    *float64        `json:"duration,omitempty"`
	Volume      *float64        `json:"volume,omitempty"`
	Muted       *bool           `json:"muted,omitempty"`
	Status      *PlaybackStatus `json:"status,omitempty"`
}

type State struct {
	mu sync.RWMutex

	version     uint64
	idleActive  bool
	paused      bool
	timePos     float64
	duration    float64
	volume      float64
	muted       bool
	playlist    []MpvPlaylistEntry
	playlistPos int

	currentItem  *QueueItem
	recentPlayed []QueueItem
	metadata     map[string]resolver.Track
	metaAddedAt  map[string]time.Time
}

type MpvPlaylistEntry struct {
	Filename string `json:"filename"`
	Current  bool   `json:"current,omitempty"`
	Playing  bool   `json:"playing,omitempty"`
	ID       int    `json:"id"`
}

func NewState() *State {
	return &State{
		metadata:    make(map[string]resolver.Track),
		metaAddedAt: make(map[string]time.Time),
		playlist:    []MpvPlaylistEntry{},
		volume:      100,
		playlistPos: -1,
	}
}

func (s *State) StoreMetadata(url string, track resolver.Track) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.metadata[url] = track
	s.metaAddedAt[url] = time.Now()
	if s.playlistPos >= 0 {
		s.currentItem = s.playlistItemLocked(s.playlistPos)
	}
	s.version++
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := StatusPlaying
	if s.idleActive {
		status = StatusIdle
	} else if s.paused {
		status = StatusPaused
	}

	queue := make([]QueueItem, len(s.playlist))
	queueIndexByID := make(map[int]int, len(s.playlist))
	for i := range s.playlist {
		item := s.playlistItemLocked(i)
		if item == nil {
			continue
		}
		queue[i] = *item
		if item.ID != 0 {
			queueIndexByID[item.ID] = i
		}
	}

	currentIdx := -1
	if status != StatusIdle && s.playlistPos >= 0 && s.playlistPos < len(s.playlist) {
		currentIdx = s.playlistPos
	}

	history := reindexRecent(s.recentPlayed, queueIndexByID)
	upcoming := []QueueItem{}
	var nowPlaying *QueueItem

	if currentIdx >= 0 {
		item := queue[currentIdx]
		nowPlaying = &item
		upcoming = append(upcoming, queue[currentIdx+1:]...)
	} else if status == StatusIdle && s.currentItem != nil {
		history = appendRecent(history, reindexItem(*s.currentItem, queueIndexByID))
	}

	return Snapshot{
		Version:     s.version,
		Status:      status,
		CurrentTime: s.timePos,
		Duration:    s.duration,
		Volume:      s.volume,
		Muted:       s.muted,
		Queue:       queue,
		History:     history,
		Upcoming:    upcoming,
		CurrentIdx:  currentIdx,
		NowPlaying:  nowPlaying,
	}
}

func (s *State) SetIdle(idle bool) {
	s.mu.Lock()
	if s.idleActive != idle {
		s.idleActive = idle
		s.version++
	}
	s.mu.Unlock()
}

func (s *State) SetPaused(paused bool) {
	s.mu.Lock()
	if s.paused != paused {
		s.paused = paused
		s.version++
	}
	s.mu.Unlock()
}

func (s *State) SetTimePos(t float64) {
	s.mu.Lock()
	s.timePos = t
	s.mu.Unlock()
}

func (s *State) SetDuration(d float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.duration = d
	if s.playlistPos >= 0 && s.playlistPos < len(s.playlist) {
		filename := s.playlist[s.playlistPos].Filename
		if track, ok := s.metadata[filename]; ok {
			track.Duration = d
			s.metadata[filename] = track
			s.currentItem = s.playlistItemLocked(s.playlistPos)
		}
	}
}

func (s *State) SetVolume(volume float64) {
	s.mu.Lock()
	if s.volume != volume {
		s.volume = volume
		s.version++
	}
	s.mu.Unlock()
}

func (s *State) SetMuted(muted bool) {
	s.mu.Lock()
	if s.muted != muted {
		s.muted = muted
		s.version++
	}
	s.mu.Unlock()
}

func (s *State) SetPlaylist(entries []MpvPlaylistEntry) {
	s.mu.Lock()
	s.playlist = entries
	s.currentItem = s.playlistItemLocked(s.playlistPos)
	s.version++
	s.mu.Unlock()
}

func (s *State) SetPlaylistPos(pos int) *QueueItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	if pos == s.playlistPos {
		if s.currentItem == nil {
			s.currentItem = s.playlistItemLocked(pos)
		}
		return s.copyCurrentItemLocked()
	}

	if s.currentItem != nil {
		s.recentPlayed = appendRecent(s.recentPlayed, *s.currentItem)
	}

	s.playlistPos = pos
	s.currentItem = s.playlistItemLocked(pos)
	s.version++
	return s.copyCurrentItemLocked()
}

func (s *State) copyCurrentItemLocked() *QueueItem {
	if s.currentItem == nil {
		return nil
	}
	cp := *s.currentItem
	return &cp
}

func (s *State) PruneMetadata() {
	s.PruneMetadataBefore(time.Time{})
}

func (s *State) PruneMetadataBefore(cutoff time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inPlaylist := make(map[string]struct{}, len(s.playlist))
	for _, entry := range s.playlist {
		inPlaylist[entry.Filename] = struct{}{}
	}

	for key := range s.metadata {
		if _, ok := inPlaylist[key]; !ok {
			if cutoff.IsZero() || s.metaAddedAt[key].Before(cutoff) {
				delete(s.metadata, key)
				delete(s.metaAddedAt, key)
			}
		}
	}
}

func (s *State) playlistItemLocked(index int) *QueueItem {
	if index < 0 || index >= len(s.playlist) {
		return nil
	}

	entry := s.playlist[index]
	item := QueueItem{
		ID:       entry.ID,
		Index:    index,
		Filename: entry.Filename,
	}

	if track, ok := s.metadata[entry.Filename]; ok {
		item.Title = track.Title
		item.Duration = track.Duration
		item.Metadata = &track
		if track.WebpageURL != "" {
			item.Filename = track.WebpageURL
		}
	}

	return &item
}

func appendRecent(items []QueueItem, item QueueItem) []QueueItem {
	if n := len(items); n > 0 && sameQueueIdentity(items[n-1], item) {
		items[n-1] = item
		return items
	}

	items = append(items, item)
	if len(items) > maxRecentPlayed {
		items = slices.Clone(items[len(items)-maxRecentPlayed:])
	}
	return items
}

func reindexRecent(items []QueueItem, queueIndexByID map[int]int) []QueueItem {
	if len(items) == 0 {
		return nil
	}

	reindexed := make([]QueueItem, len(items))
	for i, item := range items {
		reindexed[i] = reindexItem(item, queueIndexByID)
	}
	return reindexed
}

func reindexItem(item QueueItem, queueIndexByID map[int]int) QueueItem {
	if item.ID == 0 {
		return item
	}

	if idx, ok := queueIndexByID[item.ID]; ok {
		item.Index = idx
		return item
	}

	item.Index = -1
	return item
}

func sameQueueIdentity(a, b QueueItem) bool {
	if a.ID != 0 || b.ID != 0 {
		return a.ID == b.ID
	}
	return a.Filename == b.Filename && a.Title == b.Title
}

func sameQueueItem(a, b QueueItem) bool {
	return a.ID == b.ID &&
		a.Index == b.Index &&
		a.Filename == b.Filename &&
		a.Title == b.Title &&
		a.Duration == b.Duration &&
		sameTrackPtr(a.Metadata, b.Metadata)
}

func sameTrackPtr(a, b *resolver.Track) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func sameQueueItemPtr(a, b *QueueItem) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return sameQueueItem(*a, *b)
	}
}

func queueChanged(a, b []QueueItem) bool {
	return !slices.EqualFunc(a, b, sameQueueItem)
}

func SnapshotsEqual(a, b Snapshot) bool {
	return a.Status == b.Status &&
		a.CurrentTime == b.CurrentTime &&
		a.Duration == b.Duration &&
		a.Volume == b.Volume &&
		a.Muted == b.Muted &&
		a.CurrentIdx == b.CurrentIdx &&
		!queueChanged(a.Queue, b.Queue) &&
		!queueChanged(a.History, b.History) &&
		!queueChanged(a.Upcoming, b.Upcoming) &&
		sameQueueItemPtr(a.NowPlaying, b.NowPlaying)
}

func ComputeDelta(prev, curr Snapshot) *Delta {
	if prev.Version == 0 {
		return nil
	}

	idleTransition := prev.Status != curr.Status && (prev.Status == StatusIdle || curr.Status == StatusIdle)
	if idleTransition ||
		prev.CurrentIdx != curr.CurrentIdx ||
		queueChanged(prev.Queue, curr.Queue) ||
		queueChanged(prev.History, curr.History) ||
		queueChanged(prev.Upcoming, curr.Upcoming) ||
		!sameQueueItemPtr(prev.NowPlaying, curr.NowPlaying) {
		return nil
	}

	delta := &Delta{Version: curr.Version}
	changed := false

	if curr.CurrentTime != prev.CurrentTime {
		delta.CurrentTime = &curr.CurrentTime
		changed = true
	}
	if curr.Duration != prev.Duration {
		delta.Duration = &curr.Duration
		changed = true
	}
	if curr.Volume != prev.Volume {
		delta.Volume = &curr.Volume
		changed = true
	}
	if curr.Muted != prev.Muted {
		delta.Muted = &curr.Muted
		changed = true
	}
	if curr.Status != prev.Status {
		delta.Status = &curr.Status
		changed = true
	}

	if !changed {
		return nil
	}

	return delta
}
