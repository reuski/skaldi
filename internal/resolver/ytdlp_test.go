package resolver

import (
	"context"
	"os"
	"testing"
	"time"

	"skaldi/internal/bootstrap"
)

func TestResolve(t *testing.T) {
	// Only run if integration flag is set or environment variable present
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test (set INTEGRATION_TEST=1)")
	}

	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Ensure provisioned? We assume it is if running integration test.
	// But let's check shim existence.
	if _, err := os.Stat(cfg.ShimPath()); os.IsNotExist(err) {
		t.Fatalf("Shim not found at %s. Run the application once to provision.", cfg.ShimPath())
	}

	r := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test with a known video (e.g., "Rick Astley - Never Gonna Give You Up")
	url := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	tracks, err := r.Resolve(ctx, url)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(tracks) != 1 {
		t.Errorf("Expected 1 track, got %d", len(tracks))
	}

	track := tracks[0]
	if track.Title != "Rick Astley - Never Gonna Give You Up (Official Music Video)" {
		t.Logf("Warning: Title mismatch: %s", track.Title)
	}
	
	if track.Duration < 200 || track.Duration > 220 {
		t.Errorf("Duration out of expected range (approx 212s): %f", track.Duration)
	}
}
