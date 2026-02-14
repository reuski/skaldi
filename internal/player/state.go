package player

import (
	"sync"

	"skaldi/internal/resolver"
)

type PlaybackStatus string

const (
	StatusIdle    PlaybackStatus = "idle"
	StatusPlaying PlaybackStatus = "playing"
	StatusPaused  PlaybackStatus = "paused"
)

type QueueItem struct {
	Index    int             `json:"index"`
	Filename string          `json:"filename"`
	Title    string          `json:"title,omitempty"`
	Duration float64         `json:"duration,omitempty"`
	Metadata *resolver.Track `json:"metadata,omitempty"`
}

type Snapshot struct {
	Status      PlaybackStatus `json:"status"`
	CurrentTime float64        `json:"current_time"`
	Duration    float64        `json:"duration"`
	Queue       []QueueItem    `json:"queue"`
	History     []QueueItem    `json:"history"`
	Upcoming    []QueueItem    `json:"upcoming"`
	CurrentIdx  int            `json:"current_index"`
	NowPlaying  *QueueItem     `json:"now_playing,omitempty"`
}

type State struct {
	mu sync.RWMutex

	idleActive  bool
	paused      bool
	timePos     float64
	duration    float64
	playlist    []MpvPlaylistEntry
	playlistPos int

	metadata map[string]resolver.Track
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
		playlist:    []MpvPlaylistEntry{},
		playlistPos: -1,
	}
}

func (s *State) StoreMetadata(url string, track resolver.Track) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadata[url] = track
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
	history := []QueueItem{}
	upcoming := []QueueItem{}
	currentIdx := -1
	var nowPlaying *QueueItem

	if s.playlistPos >= 0 && s.playlistPos < len(s.playlist) {
		currentIdx = s.playlistPos
	} else {
		for i, entry := range s.playlist {
			if entry.Current {
				currentIdx = i
				break
			}
		}
	}

	for i, entry := range s.playlist {
		item := QueueItem{
			Index:    i,
			Filename: entry.Filename,
		}

		if track, ok := s.metadata[entry.Filename]; ok {
			item.Title = track.Title
			item.Duration = track.Duration
			item.Metadata = &track
		}

		queue[i] = item

		if i < currentIdx {
			history = append(history, item)
		} else if i > currentIdx {
			upcoming = append(upcoming, item)
		} else if i == currentIdx {
			nowPlaying = &item
		}
	}

	return Snapshot{
		Status:      status,
		CurrentTime: s.timePos,
		Duration:    s.duration,
		Queue:       queue,
		History:     history,
		Upcoming:    upcoming,
		CurrentIdx:  currentIdx,
		NowPlaying:  nowPlaying,
	}
}

func (s *State) SetIdle(idle bool) {
	s.mu.Lock()
	s.idleActive = idle
	if idle {
		s.playlistPos = -1
	}
	s.mu.Unlock()
}

func (s *State) SetPaused(paused bool) {
	s.mu.Lock()
	s.paused = paused
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
		}
	}
}

func (s *State) SetPlaylist(entries []MpvPlaylistEntry) {
	s.mu.Lock()
	s.playlist = entries
	s.mu.Unlock()
}

func (s *State) SetPlaylistPos(pos int) {
	s.mu.Lock()
	if pos != s.playlistPos {
		s.timePos = 0
		s.duration = 0
	}
	s.playlistPos = pos
	s.mu.Unlock()
}

func (s *State) PruneMetadata() {
	s.mu.Lock()
	defer s.mu.Unlock()

	inPlaylist := make(map[string]struct{}, len(s.playlist))
	for _, entry := range s.playlist {
		inPlaylist[entry.Filename] = struct{}{}
	}

	for key := range s.metadata {
		if _, ok := inPlaylist[key]; !ok {
			delete(s.metadata, key)
		}
	}
}
