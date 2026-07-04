package player

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jasonkradams/bedside-reader/internal/bus"
)

func TestMpvArgs_UsesExplicitAudioDevice(t *testing.T) {
	joined := strings.Join(mpvArgs("/tmp/x.sock"), " ")

	if !strings.Contains(joined, "--audio-device="+audioDevice) {
		t.Errorf("mpv args missing explicit audio device %q: %s", audioDevice, joined)
	}
	if !strings.Contains(joined, "--ao=alsa") {
		t.Errorf("mpv args missing --ao=alsa: %s", joined)
	}
	if !strings.Contains(joined, "--input-ipc-server=/tmp/x.sock") {
		t.Errorf("mpv args missing ipc server socket: %s", joined)
	}
}

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

func TestPlayer_Success_EndFileErrorUpdatesIdleState(t *testing.T) {
	b := bus.New()
	p := &Player{
		bus:    b,
		isIdle: false, // Player is currently playing
		State:  PlaybackState{Paused: false, Position: 100},
	}

	// We can test the logic that would normally be inside listen()
	// by refactoring or just simulating the internal state changes.
	// Actually, let's just simulate the JSON payload since listen() reads from p.conn.
	// To avoid starting mpv, we can use net.Pipe
	client, server := net.Pipe()
	p.conn = client

	go p.listen()

	// Send an end-file event with reason "error"
	_, _ = server.Write([]byte(`{"event": "end-file", "reason": "error"}` + "\n"))

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()

	if !p.isIdle {
		t.Errorf("fail: expected isIdle to be true after end-file with reason error, but got false")
	}
	if !p.State.Paused {
		t.Errorf("fail: expected State.Paused to be true after end-file with reason error, but got false")
	}

	server.Close()
}

func TestPlayer_Success_HandleTimePosUpdatesPosition(t *testing.T) {
	b := bus.New()
	p := &Player{
		bus:      b,
		State:    PlaybackState{Duration: 100},
		lastSave: time.Now(), // recent save so the periodic DB write is skipped (no lib in test)
	}

	p.handleTimePos(42.5)

	if p.State.Position != 42.5 {
		t.Errorf("fail: expected Position to be updated to 42.5, got %v", p.State.Position)
	}
}
