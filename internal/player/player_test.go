package player

import (
	"net"
	"testing"
	"time"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"periph.io/x/conn/v3/gpio"
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

type mockPin struct {
	gpio.PinOut
	level gpio.Level
}

func (m *mockPin) Out(l gpio.Level) error {
	m.level = l
	return nil
}

func TestPlayer_Success_FileLoadedWhenPausedMutes(t *testing.T) {
	b := bus.New()

	mp := &mockPin{level: gpio.Low}
	p := &Player{
		bus:     b,
		mutePin: mp,
		State:   PlaybackState{Paused: true}, // Player is PAUSED
	}
	p.sendCommandMock = func(command ...any) error { return nil }

	p.handleFileLoaded()

	// Wait 150ms since it spawns a goroutine
	time.Sleep(150 * time.Millisecond)

	if mp.level == gpio.High {
		t.Errorf("fail: expected hardware to remain muted (gpio.Low) if player is paused, but it was unmuted (gpio.High)")
	}
}

func TestPlayer_Success_TriggerMuteWhenPaused(t *testing.T) {
	mp := &mockPin{level: gpio.Low}
	p := &Player{
		mutePin: mp,
		State:   PlaybackState{Paused: true}, // Player is PAUSED
	}

	// Act
	p.triggerMute()

	// Assert
	if mp.level != gpio.Low {
		t.Errorf("fail: expected mute pin to stay Low when triggerMute is called while paused, but got %v", mp.level)
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

func TestPlayer_Success_MutesBeforeEOF(t *testing.T) {
	b := bus.New()
	mp := &mockPin{level: gpio.High}
	p := &Player{
		bus:      b,
		mutePin:  mp,
		isIdle:   false,
		State:    PlaybackState{Duration: 100, Position: 90},
		lastSave: time.Now(),
	}

	// We don't need listen(), we can just call handleTimePos(val) if we extract it,
	// or we can just simulate the JSON payload over net.Pipe.
	client, server := net.Pipe()
	p.conn = client
	go p.listen()

	// Send time-pos update close to duration
	_, _ = server.Write([]byte(`{"event": "property-change", "name": "time-pos", "data": 99.9}` + "\n"))

	time.Sleep(50 * time.Millisecond)

	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()
	
	if mp.level != gpio.Low {
		t.Errorf("fail: expected mute pin to be Low when near EOF to prevent pop, got %v", mp.level)
	}

	server.Close()
}


