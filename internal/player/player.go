package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
)

const ipcSocket = "/var/lib/bedside/mpv.sock"

// Player controls the mpv subprocess and exposes playback controls
type Player struct {
	bus         *bus.Bus
	lib         *library.Manager
	cmd         *exec.Cmd
	conn        net.Conn
	reqID       int
	reqMutex    sync.Mutex
	currentPath string
	pendingSeek float64
	lastSave    time.Time
	isIdle      bool

	mutePin   gpio.PinOut
	muteTimer *time.Timer

	State PlaybackState

	// Test hook
	sendCommandMock func(command ...any) error
}

// triggerMute temporarily pulls the hardware mute pin low to mask I2S clock transients
func (p *Player) triggerMute() {
	if p.mutePin != nil && !p.State.Paused {
		p.mutePin.Out(gpio.Low)
		if p.muteTimer != nil {
			p.muteTimer.Stop()
		}
		p.muteTimer = time.AfterFunc(100*time.Millisecond, func() {
			p.mutePin.Out(gpio.High)
		})
	}
}

// PlaybackState represents the current state of the player
type PlaybackState struct {
	FilePath string
	Paused   bool
	Position float64
	Duration float64
	Volume   float64
}

// New starts the mpv subprocess and connects to its IPC socket
func New(eventBus *bus.Bus, lib *library.Manager) (*Player, error) {
	os.Remove(ipcSocket)

	pin := gpioreg.ByName("GPIO26")
	if pin != nil {
		pin.Out(gpio.Low) // Start muted
	}

	p := &Player{
		bus:         eventBus,
		lib:         lib,
		currentPath: "",
		mutePin:     pin,
		State:       PlaybackState{Volume: 50, Paused: true},
	}

	// Start mpv as a background daemon
	p.cmd = exec.Command("mpv",
		"--idle",
		"--no-video",
		"--really-quiet",
		"--no-config",
		"--ao=alsa",
		fmt.Sprintf("--input-ipc-server=%s", ipcSocket),
	)

	if err := p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mpv: %w", err)
	}

	// Wait for the socket to be created
	var conn net.Conn
	var err error
	for range 50 {
		time.Sleep(100 * time.Millisecond)
		conn, err = net.Dial("unix", ipcSocket)
		if err == nil {
			break
		}
	}
	if err != nil {
		if p.cmd.Process != nil {
			p.cmd.Process.Kill()
		}
		return nil, fmt.Errorf("failed to connect to mpv IPC: %w", err)
	}
	p.conn = conn

	go p.listen()

	// Observe properties so mpv tells us when they change
	p.observeProperty("time-pos")
	p.observeProperty("duration")
	p.observeProperty("volume")

	// Apply default volume
	p.SetVolume(p.State.Volume)

	return p, nil
}

// listen reads events and property changes from mpv
func (p *Player) listen() {
	scanner := bufio.NewScanner(p.conn)
	for scanner.Scan() {
		line := scanner.Bytes()

		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if event, ok := msg["event"].(string); ok {
			switch event {
			case "file-loaded":
				p.handleFileLoaded()
			case "end-file":
				p.reqMutex.Lock()
				// Only trigger end-file if it actually finished playing naturally
				if reason, ok := msg["reason"].(string); ok && reason == "eof" {
					p.isIdle = true
					p.State.Paused = true
					p.State.Position = 0
					p.lib.SaveProgress(p.State.FilePath, 0)
					p.bus.Publish(bus.EventPlayerStateChanged, p.State)
				}
				p.reqMutex.Unlock()
			case "property-change":
				name, _ := msg["name"].(string)
				data := msg["data"]

				changed := false
				switch name {
				case "time-pos":
					if val, ok := data.(float64); ok {
						p.State.Position = val
						p.bus.Publish(bus.EventPlayerProgressTick, p.State)
						if time.Since(p.lastSave) > 10*time.Second {
							p.lib.SaveProgress(p.State.FilePath, p.State.Position)
							_, _, timeout, _ := p.lib.GetSystemState()
							p.lib.SaveSystemState(p.currentPath, !p.State.Paused, timeout)
							p.lastSave = time.Now()
						}
					}
				case "duration":
					if val, ok := data.(float64); ok {
						p.State.Duration = val
						changed = true
					}
				case "volume":
					if val, ok := data.(float64); ok {
						p.State.Volume = val
						changed = true
					}
				}

				if changed {
					p.bus.Publish(bus.EventPlayerStateChanged, p.State)
				}
			}
		}
	}
}

func (p *Player) handleFileLoaded() {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()
	p.isIdle = false

	// Unmute the audio now that the file is fully loaded and ready to play
	p.sendCommandNoLock("set_property", "mute", false)
	if p.mutePin != nil && !p.State.Paused {
		// Delay unmute to allow ALSA stream to settle (masks the mpv load pop)
		go func() {
			time.Sleep(100 * time.Millisecond)
			p.mutePin.Out(gpio.High) // Hardware Unmute
		}()
	}

	if p.pendingSeek > 0 {
		p.sendCommandNoLock("seek", p.pendingSeek, "absolute", "exact")
		p.pendingSeek = 0
	}
}

// sendCommand sends a JSON IPC command to mpv
func (p *Player) sendCommand(command ...any) error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()
	return p.sendCommandNoLock(command...)
}

func (p *Player) sendCommandNoLock(command ...any) error {
	if p.sendCommandMock != nil {
		return p.sendCommandMock(command...)
	}

	p.reqID++
	req := map[string]any{
		"command":    command,
		"request_id": p.reqID,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	data = append(data, '\n')
	_, err = p.conn.Write(data)
	return err
}

func (p *Player) observeProperty(name string) {
	p.sendCommand("observe_property", p.reqID, name)
}

// LoadFile tells mpv to load a new file
func (p *Player) LoadFile(path string) error {
	// 1. Save progress of CURRENT file before switching
	if p.State.FilePath != "" && p.State.Position > 0 {
		p.lib.SaveProgress(p.State.FilePath, p.State.Position)
		_, _, timeout, _ := p.lib.GetSystemState()
		p.lib.SaveSystemState(p.currentPath, !p.State.Paused, timeout)
	}

	// 3. Update state for new file
	p.currentPath = path
	p.State.FilePath = filepath.Base(path)
	_, _, timeout, _ := p.lib.GetSystemState()
	p.lib.SaveSystemState(path, true, timeout)

	// 4. Load saved progress for the NEW file
	if pos, err := p.lib.GetProgress(p.State.FilePath); err == nil && pos > 0 {
		p.State.Position = pos
		p.pendingSeek = pos
	} else {
		p.State.Position = 0
		p.pendingSeek = 0
	}

	// 5. Mute output instantly to hide transition buzzing, will unmute on file-loaded
	p.sendCommand("set_property", "mute", true)
	if p.mutePin != nil {
		p.mutePin.Out(gpio.Low) // Keep mute until Play/Resume
		if p.muteTimer != nil {
			p.muteTimer.Stop()
		}
	} // Hardware Mute

	p.State.Paused = false
	p.bus.Publish(bus.EventPlayerStateChanged, p.State)
	p.sendCommand("set_property", "pause", false)
	return p.sendCommand("loadfile", path, "replace")
}

// TogglePause toggles playback state using Deep Sleep (stops mpv to close ALSA device)
func (p *Player) TogglePause() error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()

	if p.isIdle {
		p.reqMutex.Unlock()
		err := p.LoadFile(p.currentPath)
		p.reqMutex.Lock()
		return err
	}

	if p.State.Paused {
		// Resume playing
		p.State.Paused = false
		_, _, timeout, _ := p.lib.GetSystemState()
		p.lib.SaveSystemState(p.currentPath, true, timeout)
		p.sendCommandNoLock("set_property", "pause", false)
		p.triggerMute()
	} else {
		// Pause native stream (keeps ALSA open, stopping I2S clock pops!)
		p.State.Paused = true
		_, _, timeout, _ := p.lib.GetSystemState()
		p.lib.SaveSystemState(p.currentPath, false, timeout)
		p.sendCommandNoLock("set_property", "pause", true)

		if p.mutePin != nil {
			p.mutePin.Out(gpio.Low) // Hardware Mute (Kills DAC hiss!)
			if p.muteTimer != nil {
				p.muteTimer.Stop()
			}
		}

		// Immediately save progress to disk
		p.lib.SaveProgress(p.State.FilePath, p.State.Position)
	}

	p.bus.Publish(bus.EventPlayerStateChanged, p.State)
	return nil
}

// Seek moves the playback position by delta seconds
func (p *Player) Seek(deltaSeconds float64) error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()

	p.triggerMute()
	return p.sendCommandNoLock("seek", deltaSeconds, "relative", "exact")
}

// SkipChapter skips forward or backward by chapters
func (p *Player) SkipChapter(delta int) error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()

	p.triggerMute()
	return p.sendCommandNoLock("add", "chapter", delta)
}

// SetVolume sets the absolute volume (0-100)
func (p *Player) SetVolume(volume float64) error {
	if volume < 0 {
		volume = 0
	} else if volume > 100 {
		volume = 100
	}
	return p.sendCommand("set_property", "volume", volume)
}

// Close kills the mpv process
func (p *Player) Close() {
	if p.conn != nil {
		p.conn.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
}
