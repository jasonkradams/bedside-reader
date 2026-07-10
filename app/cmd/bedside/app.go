package main

import (
	"log"
	"path/filepath"
	"time"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/display"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
	"github.com/jasonkradams/bedside-reader/internal/ui"
)

// App orchestrates the high-level application state, including UI menus,
// screen timeouts, and input handling logic.
type App struct {
	bus    *bus.Bus
	lib    *library.Manager
	gui    *ui.Renderer
	player *player.Player

	// State
	encoderMode       string // "vol" or "scrub"
	inMenu            bool
	menuIndex         int
	menuBooks         []library.Audiobook
	screenOff         bool
	screenTimeoutMins int

	// Timers
	screenTimer        *time.Timer
	singleClickTimer   *time.Timer
	lastEncoderBtnTime time.Time
}

// NewApp creates a new App controller
func NewApp(b *bus.Bus, lib *library.Manager, gui *ui.Renderer, p *player.Player, sysState library.SystemState) *App {
	a := &App{
		bus:               b,
		lib:               lib,
		gui:               gui,
		player:            p,
		inMenu:            sysState.ActiveFile == "",
		screenTimeoutMins: sysState.Timeout,
		encoderMode:       sysState.EncoderMode,
	}

	if a.encoderMode != "vol" && a.encoderMode != "scrub" {
		a.encoderMode = "vol"
	}

	_ = p.SetVolume(sysState.Volume)
	a.gui.SetEncoderMode(a.encoderMode)

	return a
}

// Run starts the main event loop
func (a *App) publishMain() {
	a.gui.SetEncoderMode(a.encoderMode)
	a.bus.Publish(bus.EventPlayerStateChanged, a.player.State)
}

func (a *App) Run() {
	ch := a.bus.Subscribe()
	a.resetScreen(true) // Start the timer and wake screen

	for ev := range ch {
		switch ev.Type {
		case bus.EventButtonPlayPause:
			a.handlePlayPause()
		case "encoder-single-click":
			a.handleEncoderSingleClick()
		case "screen-timeout":
			a.handleScreenTimeout()
		case bus.EventButtonSkipFwd:
			a.handleSkipFwd()
		case bus.EventButtonSkipBack:
			a.handleSkipBack()
		case bus.EventButtonMenu:
			a.handleMenu()
		case bus.EventEncoderBtn:
			a.handleEncoderBtn()
		case bus.EventEncoderTurn:
			if delta, ok := ev.Payload.(int); ok {
				a.handleEncoderTurn(delta)
			}
		case bus.EventLibraryScanComplete:
			log.Println("Received EventLibraryScanComplete")
		}
	}
}

func (a *App) resetScreen(wake bool) bool {
	wasOff := a.screenOff
	if wake {
		a.screenOff = false
		display.SetBacklight(true)
		if wasOff {
			// Coming back from a blanked screen: redraw immediately so the panel
			// shows current state rather than whatever frame was left when it slept.
			a.refreshDisplay()
		}
	}

	if a.screenTimer != nil {
		a.screenTimer.Stop()
	}
	if a.screenTimeoutMins > 0 {
		a.screenTimer = time.AfterFunc(time.Duration(a.screenTimeoutMins)*time.Minute, func() {
			a.bus.Publish("screen-timeout", nil)
		})
	}
	return wasOff
}

func (a *App) publishMenu() {
	a.bus.Publish(bus.EventMenuUpdate, bus.MenuState{
		Active: a.inMenu,
		Books:  a.menuBooks,
		Index:  a.menuIndex,
	})
}

// refreshDisplay re-publishes current state so the UI redraws immediately, e.g.
// when the screen wakes from a blanked idle and must not show a stale frame.
func (a *App) refreshDisplay() {
	a.gui.SetEncoderMode(a.encoderMode)
	a.bus.Publish(bus.EventPlayerStateChanged, a.player.State)
	a.publishMenu()
}

func (a *App) handlePlayPause() {
	log.Println("Received EventButtonPlayPause")
	if a.resetScreen(true) {
		return
	}
	if a.inMenu {
		if a.menuIndex > 0 && a.menuIndex-1 < len(a.menuBooks) {
			book := a.menuBooks[a.menuIndex-1]
			a.player.LoadFile(book.FilePath)
			a.inMenu = false
			a.publishMenu()
		}
	} else {
		a.player.TogglePause()
	}
}

func (a *App) handleEncoderSingleClick() {
	// Edge case: single click does not wake screen, so it doesn't matter if it's off.
	if a.inMenu && !a.screenOff {
		if a.menuIndex == 0 {
			a.cycleScreenTimeout()
		} else if len(a.menuBooks) > 0 {
			bookIdx := a.menuIndex - 1
			if bookIdx >= 0 && bookIdx < len(a.menuBooks) {
				a.player.LoadFile(a.menuBooks[bookIdx].FilePath)
				a.inMenu = false
				a.publishMenu()
			}
		}
	} else {
		a.handleEncoderToggle()
	}
}

// defaultScreenTimeout is used when the stored value isn't one of the options.
const defaultScreenTimeout = 5

// screenTimeoutCycle is the ordered ring the settings UI steps through, and the
// single source of truth: reorder or add an option here and nextScreenTimeout follows.
var screenTimeoutCycle = []int{0, 1, 5, 15, 60}

// nextScreenTimeout returns the option after current in screenTimeoutCycle,
// wrapping past the end back to the start. An unrecognized current falls back
// to defaultScreenTimeout.
func nextScreenTimeout(current int) int {
	for i, v := range screenTimeoutCycle {
		if v == current {
			return screenTimeoutCycle[(i+1)%len(screenTimeoutCycle)]
		}
	}
	return defaultScreenTimeout
}

func (a *App) cycleScreenTimeout() {
	a.screenTimeoutMins = nextScreenTimeout(a.screenTimeoutMins)
	sysState, _ := a.lib.GetSystemState()
	sysState.Timeout = a.screenTimeoutMins
	_ = a.lib.SaveSystemState(sysState)
	a.resetScreen(true)
	a.publishMenu()
}

func (a *App) handleScreenTimeout() {
	a.screenOff = true
	display.SetBacklight(false)
}

func (a *App) handleEncoderToggle() {
	log.Println("Received EventButtonEncoderToggle")
	if a.resetScreen(true) {
		return
	}
	if a.inMenu {
		return // Do nothing in menu
	}

	if a.encoderMode == "scrub" {
		a.encoderMode = "vol"
	} else {
		a.encoderMode = "scrub"
	}

	// Save the encoder mode to persistence
	sysState, _ := a.lib.GetSystemState()
	sysState.EncoderMode = a.encoderMode
	_ = a.lib.SaveSystemState(sysState)

	a.publishMain()
}

func (a *App) handleSkipFwd() {
	log.Println("Received EventButtonSkipFwd")
	if a.resetScreen(true) {
		return
	}
	if a.inMenu {
		if a.menuIndex > 0 {
			a.menuIndex--
			a.publishMenu()
		}
		return
	}
	if a.atLastChapter() {
		return // Don't skip forward past the final chapter
	}
	_ = a.player.SkipChapter(1)
}

// atLastChapter reports whether playback is within the final chapter of the
// current book, used to suppress skip-forward past the end.
func (a *App) atLastChapter() bool {
	b, err := a.lib.GetByFilename(a.player.State.FilePath)
	if err != nil {
		return false
	}
	return library.ChapterIndexAt(b.Chapters, a.player.State.Position) >= len(b.Chapters)-1
}

func (a *App) handleSkipBack() {
	log.Println("Received EventButtonSkipBack")
	if a.resetScreen(true) {
		return
	}
	if a.inMenu {
		if a.menuIndex < len(a.menuBooks) {
			a.menuIndex++
			a.publishMenu()
		}
	} else {
		a.player.SkipChapter(-1)
	}
}

func (a *App) handleMenu() {
	log.Println("Received EventButtonMenu")
	if a.resetScreen(true) {
		return
	}
	a.inMenu = !a.inMenu
	if a.inMenu {
		a.menuBooks, _ = a.lib.GetAll()
		currentPath := a.player.State.FilePath
		a.menuIndex = 1 // 0 is settings
		for i, b := range a.menuBooks {
			if filepath.Base(b.FilePath) == filepath.Base(currentPath) {
				a.menuIndex = i + 1
				break
			}
		}
	}
	a.publishMenu()
}

func (a *App) handleEncoderBtn() {
	now := time.Now()
	if now.Sub(a.lastEncoderBtnTime) < 400*time.Millisecond {
		// Double click
		if a.singleClickTimer != nil {
			a.singleClickTimer.Stop()
		}

		if a.screenOff {
			a.resetScreen(true)
		} else {
			a.screenOff = true
			display.SetBacklight(false)
			if a.screenTimer != nil {
				a.screenTimer.Stop()
			}
		}
		a.lastEncoderBtnTime = time.Time{} // reset
	} else {
		// Schedule single click
		a.resetScreen(false) // Reset timer, don't wake
		a.singleClickTimer = time.AfterFunc(400*time.Millisecond, func() {
			a.bus.Publish("encoder-single-click", nil)
		})
		a.lastEncoderBtnTime = now
	}
}

func (a *App) handleEncoderTurn(delta int) {
	a.resetScreen(false) // Reset timer, don't wake
	if a.inMenu && !a.screenOff {
		a.handleMenuScroll(delta)
	} else if a.encoderMode == "scrub" {
		a.handleScrub(delta)
	} else {
		a.handleVolumeChange(delta)
	}
}

func (a *App) handleMenuScroll(delta int) {
	if delta > 0 && a.menuIndex < len(a.menuBooks) {
		a.menuIndex++
		a.publishMenu()
	} else if delta < 0 && a.menuIndex > 0 {
		a.menuIndex--
		a.publishMenu()
	}
}

func (a *App) handleScrub(delta int) {
	_ = a.player.Seek(float64(delta * 15))
}

func (a *App) handleVolumeChange(delta int) {
	newVol := a.player.State.Volume + float64(delta*5)
	_ = a.player.SetVolume(newVol)

	// Persist the volume setting
	sysState, _ := a.lib.GetSystemState()
	sysState.Volume = newVol
	if sysState.Volume < 0 {
		sysState.Volume = 0
	} else if sysState.Volume > 100 {
		sysState.Volume = 100
	}
	_ = a.lib.SaveSystemState(sysState)
}
