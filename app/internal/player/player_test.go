package player

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/library"
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

// newTestPlayer builds a Player wired to a bus and a temp on-disk library, for
// exercising handlers that persist state.
func newTestPlayer(t *testing.T) *Player {
	t.Helper()
	dir := t.TempDir()
	b := bus.New()
	lib, err := library.New(b, filepath.Join(dir, "l.db"), filepath.Join(dir, "a"), filepath.Join(dir, "c"))
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	t.Cleanup(lib.Close)
	return &Player{bus: b, lib: lib}
}

// TestPlayer_HandleEndFile covers the end-of-book / book-switch fixes: only a
// natural eof or a decode error stops playback; end-file caused by our own
// load/replace (stop/redirect) or arriving mid-load must be ignored so it can't
// clobber the incoming file's state.
func TestPlayer_HandleEndFile(t *testing.T) {
	cases := []struct {
		name       string
		reason     string
		loading    bool
		wantIdle   bool
		wantPaused bool
		wantRewind bool
	}{
		{"eof rewinds and idles", "eof", false, true, true, true},
		{"error idles without rewind", "error", false, true, true, false},
		{"stop from our own load is ignored", "stop", false, false, false, false},
		{"redirect is ignored", "redirect", false, false, false, false},
		{"eof during a load is ignored", "eof", true, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPlayer(t)
			p.loading = tc.loading
			p.State = PlaybackState{FilePath: "b.m4b", Paused: false, Position: 123}

			p.handleEndFile(map[string]any{"event": "end-file", "reason": tc.reason})

			if p.isIdle != tc.wantIdle {
				t.Errorf("isIdle = %v, want %v", p.isIdle, tc.wantIdle)
			}
			if p.State.Paused != tc.wantPaused {
				t.Errorf("Paused = %v, want %v", p.State.Paused, tc.wantPaused)
			}
			if rewound := p.State.Position == 0; rewound != tc.wantRewind {
				t.Errorf("Position = %v (rewound=%v), want rewind=%v", p.State.Position, rewound, tc.wantRewind)
			}
		})
	}
}

// TestPlayer_HandlePauseChange verifies the observed pause property is the source
// of truth: it updates State and notifies the UI.
func TestPlayer_HandlePauseChange(t *testing.T) {
	b := bus.New()
	sub := b.Subscribe()
	p := &Player{bus: b, State: PlaybackState{Paused: false}}

	p.handlePauseChange(true)

	if !p.State.Paused {
		t.Error("State.Paused = false, want true after observing pause=true")
	}
	select {
	case ev := <-sub:
		if ev.Type != bus.EventPlayerStateChanged {
			t.Errorf("published %q, want %q", ev.Type, bus.EventPlayerStateChanged)
		}
	case <-time.After(time.Second):
		t.Error("no state-changed event published on pause change")
	}
}
