package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/library"
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

	State PlaybackState
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
	p := &Player{
		bus:   eventBus,
		lib:   lib,
		State: PlaybackState{Volume: 50, Paused: true},
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
	// We no longer observe "pause" because we manage it manually for deep sleep
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
			if event == "file-loaded" {
				p.reqMutex.Lock()
				if p.pendingSeek > 0 {
					p.sendCommandNoLock("seek", p.pendingSeek, "absolute", "exact")
					p.pendingSeek = 0
				}
				p.reqMutex.Unlock()
			} else if event == "property-change" {
				name, _ := msg["name"].(string)
				data := msg["data"]

				changed := false
				switch name {
				case "time-pos":
					if val, ok := data.(float64); ok {
						if !p.State.Paused {
							p.State.Position = val
							p.bus.Publish(bus.EventPlayerProgressTick, p.State)
							if time.Since(p.lastSave) > 10*time.Second {
								p.lib.SaveProgress(p.State.FilePath, p.State.Position)
								p.lastSave = time.Now()
							}
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

// sendCommand sends a JSON IPC command to mpv
func (p *Player) sendCommand(command ...any) error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()
	return p.sendCommandNoLock(command...)
}

func (p *Player) sendCommandNoLock(command ...any) error {
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
	}

	// 2. Send stop to kill the ALSA output instantly (prevents buzzing)
	p.sendCommand("stop")

	// 3. Update state for new file
	p.currentPath = path
	p.State.FilePath = filepath.Base(path)
	
	// 4. Load saved progress for the NEW file
	if pos, err := p.lib.GetProgress(p.State.FilePath); err == nil && pos > 0 {
		p.State.Position = pos
		p.pendingSeek = pos
	} else {
		p.State.Position = 0
		p.pendingSeek = 0
	}
	
	p.State.Paused = false
	p.bus.Publish(bus.EventPlayerStateChanged, p.State)
	return p.sendCommand("loadfile", path, "replace")
}

// TogglePause toggles playback state using Deep Sleep (stops mpv to close ALSA device)
func (p *Player) TogglePause() error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()

	if p.State.Paused {
		// Resume playing
		p.State.Paused = false
		p.pendingSeek = p.State.Position
		p.sendCommandNoLock("loadfile", p.currentPath, "replace")
	} else {
		// Deep sleep pause (closes ALSA device and kills DAC noise)
		p.State.Paused = true
		p.sendCommandNoLock("stop")
		
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

	// If paused, we just update the internal state so when we resume it starts at the new position
	if p.State.Paused {
		p.State.Position += deltaSeconds
		if p.State.Position < 0 {
			p.State.Position = 0
		}
		p.bus.Publish(bus.EventPlayerStateChanged, p.State)
		return nil
	}

	return p.sendCommandNoLock("seek", deltaSeconds, "relative", "exact")
}

// SkipChapter skips forward or backward by chapters
func (p *Player) SkipChapter(delta int) error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()

	if p.State.Paused {
		// If paused, we manually calculate the target chapter start time from the DB
		if p.State.FilePath == "" {
			return nil
		}
		book, err := p.lib.GetByFilename(p.State.FilePath)
		if err != nil || len(book.Chapters) == 0 {
			return nil
		}

		// Find current chapter index based on position
		currentIdx := 0
		for i, chap := range book.Chapters {
			// Add a small epsilon (0.5s) to avoid getting stuck on the boundary
			if p.State.Position >= chap.StartTime-0.5 {
				currentIdx = i
			} else {
				break
			}
		}

		targetIdx := currentIdx + delta
		if targetIdx < 0 {
			targetIdx = 0
		} else if targetIdx >= len(book.Chapters) {
			targetIdx = len(book.Chapters) - 1
		}

		p.State.Position = book.Chapters[targetIdx].StartTime
		p.bus.Publish(bus.EventPlayerStateChanged, p.State)
		return nil
	}

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
