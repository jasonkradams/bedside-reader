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
)

const ipcSocket = "/var/lib/bedside/mpv.sock"

// audioDevice pins mpv to the MAX98357A I2S DAC by its stable ALSA card name.
// The ALSA "default" PCM on this build can resolve to a nonexistent card, so a
// bare --ao=alsa fails to open any device ("Unknown PCM default"); naming the
// card explicitly and routing through "plug" (for rate/format conversion of
// arbitrary source audio) is what actually produces sound. The MAX98357A's
// enable pin (GPIO26) is owned by the kernel max98357a driver via the DT overlay
// (sdmode-pin=26) and is driven automatically by DAPM on stream start/stop — the
// app deliberately does not touch it (doing so races the kernel and mutes audio).
const audioDevice = "alsa/plughw:CARD=MAX98357A,DEV=0"

// mpvArgs builds the mpv daemon argument list. Split out from New so the audio
// device selection is unit-testable without launching mpv.
func mpvArgs(socket string) []string {
	return []string{
		"--idle",
		"--no-video",
		"--really-quiet",
		"--no-config",
		"--ao=alsa",
		"--audio-device=" + audioDevice,
		"--input-ipc-server=" + socket,
	}
}

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

	State PlaybackState

	// Test hook
	sendCommandMock func(command ...any) error
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

	p := &Player{
		bus:         eventBus,
		lib:         lib,
		currentPath: "",
		State:       PlaybackState{Volume: 50, Paused: true},
	}

	// Start mpv as a background daemon
	p.cmd = exec.Command("mpv", mpvArgs(ipcSocket)...)

	if err := p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mpv: %w", err)
	}

	// Wait for the socket to be created
	var conn net.Conn
	var err error
	for range 150 {
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

// listen reads newline-delimited JSON events from mpv and dispatches them.
func (p *Player) listen() {
	scanner := bufio.NewScanner(p.conn)
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		p.handleEvent(msg)
	}
}

func (p *Player) handleEvent(msg map[string]any) {
	event, ok := msg["event"].(string)
	if !ok {
		return
	}
	switch event {
	case "file-loaded":
		p.handleFileLoaded()
	case "end-file":
		p.handleEndFile(msg)
	case "property-change":
		p.handlePropertyChange(msg)
	}
}

func (p *Player) handleEndFile(msg map[string]any) {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()

	p.isIdle = true
	p.State.Paused = true
	// Only reset progress to 0 if the file finished playing naturally.
	if reason, ok := msg["reason"].(string); ok && reason == "eof" {
		p.State.Position = 0
		p.lib.SaveProgress(p.State.FilePath, 0)
	}
	p.bus.Publish(bus.EventPlayerStateChanged, p.State)
}

func (p *Player) handlePropertyChange(msg map[string]any) {
	name, _ := msg["name"].(string)
	data := msg["data"]

	switch name {
	case "time-pos":
		if val, ok := data.(float64); ok {
			p.handleTimePos(val)
		}
	case "duration":
		if val, ok := data.(float64); ok {
			p.State.Duration = val
			p.bus.Publish(bus.EventPlayerStateChanged, p.State)
		}
	case "volume":
		if val, ok := data.(float64); ok {
			p.State.Volume = val
			p.bus.Publish(bus.EventPlayerStateChanged, p.State)
		}
	}
}

// handleTimePos processes a playback position update: it broadcasts progress and
// throttles progress persistence.
func (p *Player) handleTimePos(val float64) {
	p.State.Position = val
	p.bus.Publish(bus.EventPlayerProgressTick, p.State)
	p.persistProgressPeriodically()
}

// persistProgressPeriodically saves playback position and system state to disk
// at most once every 10 seconds to limit write churn.
func (p *Player) persistProgressPeriodically() {
	if time.Since(p.lastSave) <= 10*time.Second {
		return
	}
	p.lib.SaveProgress(p.State.FilePath, p.State.Position)
	sysState, _ := p.lib.GetSystemState()
	sysState.ActiveFile = p.currentPath
	sysState.Playing = !p.State.Paused
	_ = p.lib.SaveSystemState(sysState)
	p.lastSave = time.Now()
}

func (p *Player) handleFileLoaded() {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()
	p.isIdle = false

	// Unmute the digital stream now that the file is fully loaded and ready to
	// play (it was muted during the load transition to mask the buffer-fill pop).
	p.sendCommandNoLock("set_property", "mute", false)

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
	p.saveCurrentState()
	p.loadNewState(path)
	p.muteForTransition()

	p.State.Paused = false
	p.bus.Publish(bus.EventPlayerStateChanged, p.State)
	_ = p.sendCommand("set_property", "pause", false)
	return p.sendCommand("loadfile", path, "replace")
}

func (p *Player) saveCurrentState() {
	if p.State.FilePath != "" && p.State.Position > 0 {
		_ = p.lib.SaveProgress(p.State.FilePath, p.State.Position)
		sysState, _ := p.lib.GetSystemState()
		sysState.ActiveFile = p.currentPath
		sysState.Playing = !p.State.Paused
		_ = p.lib.SaveSystemState(sysState)
	}
}

func (p *Player) loadNewState(path string) {
	p.currentPath = path
	p.State.FilePath = filepath.Base(path)

	sysState, _ := p.lib.GetSystemState()
	sysState.ActiveFile = path
	sysState.Playing = true
	_ = p.lib.SaveSystemState(sysState)

	if pos, err := p.lib.GetProgress(p.State.FilePath); err == nil && pos > 0 {
		p.State.Position = pos
		p.pendingSeek = pos
	} else {
		p.State.Position = 0
		p.pendingSeek = 0
	}
}

// muteForTransition mutes mpv's digital output while a new file is loading, so
// the buffer-fill pop is masked. handleFileLoaded unmutes once playback is ready.
func (p *Player) muteForTransition() {
	_ = p.sendCommand("set_property", "mute", true)
}

// TogglePause toggles playback state
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
		p.resume()
	} else {
		p.pause()
	}

	p.bus.Publish(bus.EventPlayerStateChanged, p.State)
	return nil
}

func (p *Player) resume() {
	p.State.Paused = false
	sysState, _ := p.lib.GetSystemState()
	sysState.ActiveFile = p.currentPath
	sysState.Playing = true
	_ = p.lib.SaveSystemState(sysState)
	_ = p.sendCommandNoLock("set_property", "pause", false)
}

func (p *Player) pause() {
	p.State.Paused = true
	sysState, _ := p.lib.GetSystemState()
	sysState.ActiveFile = p.currentPath
	sysState.Playing = false
	_ = p.lib.SaveSystemState(sysState)
	_ = p.sendCommandNoLock("set_property", "pause", true)

	// Immediately save progress to disk
	_ = p.lib.SaveProgress(p.State.FilePath, p.State.Position)
}

// Seek moves the playback position by delta seconds
func (p *Player) Seek(deltaSeconds float64) error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()
	return p.sendCommandNoLock("seek", deltaSeconds, "relative", "exact")
}

// SkipChapter skips forward or backward by chapters
func (p *Player) SkipChapter(delta int) error {
	p.reqMutex.Lock()
	defer p.reqMutex.Unlock()
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
