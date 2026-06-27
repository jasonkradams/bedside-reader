package player

import (
	"testing"

	"github.com/jasonkradams/bedside-reader/internal/bus"
)

func TestPlayer_Success_SeekWhenPaused(t *testing.T) {
	// Setup dummy player
	b := bus.New()
	p := &Player{
		bus:   b,
		State: PlaybackState{Paused: true, Position: 100},
	}

	seekSent := false
	p.sendCommandMock = func(command ...any) error {
		if len(command) > 0 && command[0] == "seek" {
			seekSent = true
		}
		return nil
	}

	// Act
	err := p.Seek(15)
	if err != nil {
		t.Fatalf("Seek failed: %v", err)
	}

	// Assert
	if !seekSent {
		t.Errorf("fail: expected seek command to be sent to mpv even when paused, but it was swallowed")
	}
}
