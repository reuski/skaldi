package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type State struct {
	Uv    string `json:"uv"`
	Bun   string `json:"bun"`
	YtDlp string `json:"yt-dlp"`
}

func LoadState(cacheDir string) (*State, error) {
	data, err := os.ReadFile(filepath.Join(cacheDir, "versions.json"))
	if os.IsNotExist(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func SaveState(cacheDir string, s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, "versions.json"), data, 0644)
}
