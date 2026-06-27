package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/display"
	"github.com/jasonkradams/bedside-reader/internal/input"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
	"github.com/jasonkradams/bedside-reader/internal/ui"
)

func main() {
	log.Println("Starting Bedside Audiobook Appliance (Native Framebuffer Mode)...")

	// 1. Start the Event Bus
	eventBus := bus.New()

	// 2. Start the Library Manager
	lib, err := library.New(eventBus, "/var/lib/bedside/library.db", "/var/lib/bedside/audiobooks", "/var/lib/bedside/covers")
	if err != nil {
		log.Fatalf("Failed to initialize library: %v", err)
	}
	defer lib.Close()

	// 3. Start the UI Renderer
	gui, err := ui.New(eventBus, lib)
	if err != nil {
		log.Fatalf("Failed to initialize UI: %v", err)
	}
	defer gui.Close()

	// 4. Start the Input Manager (Buttons)
	_, err = input.New(eventBus)
	if err != nil {
		log.Printf("Warning: Input manager failed to initialize: %v", err)
	}

	// 5. Start the Audio Player (mpv)
	mpv, err := player.New(eventBus, lib)
	if err != nil {
		log.Fatalf("Failed to start player: %v", err)
	}
	defer mpv.Close()

	// Resume System State!
	activePath, playing, screenTimeoutMins, err := lib.GetSystemState()
	if err == nil && activePath != "" {
		log.Printf("Resuming state: %s (Playing: %v, Timeout: %dm)", activePath, playing, screenTimeoutMins)
		mpv.LoadFile(activePath)
		if !playing {
			mpv.TogglePause() // LoadFile automatically plays, so pause if it wasn't playing
		}
	} else {
		log.Println("No saved system state, idling.")
	}

	// 6. Connect Input to Player logic
	ch := eventBus.Subscribe()
	go func() {
		scrubMode := false
		var lastEncoderBtnTime time.Time
		var singleClickTimer *time.Timer
		var screenTimer *time.Timer
		screenOff := false

		resetScreen := func(wake bool) bool {
			wasOff := screenOff
			if wake {
				screenOff = false
				display.SetBacklight(true)
			}

			if screenTimer != nil {
				screenTimer.Stop()
			}
			if screenTimeoutMins > 0 {
				screenTimer = time.AfterFunc(time.Duration(screenTimeoutMins)*time.Minute, func() {
					eventBus.Publish("screen-timeout", nil)
				})
			}
			return wasOff
		}

		resetScreen(true) // Start the timer and wake screen

		inMenu := false
		menuIndex := 0
		var menuBooks []library.Audiobook

		publishMenu := func() {
			eventBus.Publish(bus.EventMenuUpdate, bus.MenuState{
				Active: inMenu,
				Books:  menuBooks,
				Index:  menuIndex,
			})
		}

		for ev := range ch {
			switch ev.Type {
			case bus.EventButtonPlayPause:
				log.Println("Received EventButtonPlayPause")
				if resetScreen(true) {
					continue
				}
				mpv.TogglePause()
			case "encoder-single-click":
				// Edge case: single click does not wake screen, so it doesn't matter if it's off.
				// However, you said if screen is off, turning or clicking should just do it without waking.
				// So we don't return early!
				if inMenu && !screenOff {
					// Cycle timeout: 0 (Off) -> 1 -> 5 -> 15 -> 60 -> 0
					if menuIndex == 0 {
						switch screenTimeoutMins {
						case 0:
							screenTimeoutMins = 1
						case 1:
							screenTimeoutMins = 5
						case 5:
							screenTimeoutMins = 15
						case 15:
							screenTimeoutMins = 60
						case 60:
							screenTimeoutMins = 0
						default:
							screenTimeoutMins = 5
						}

						// Save it immediately
						lib.SaveSystemState(mpv.State.FilePath, !mpv.State.Paused, screenTimeoutMins)

						resetScreen(true)
						publishMenu() // Refresh menu
					} else if len(menuBooks) > 0 {
						// Play book! (menuIndex-1 because index 0 is settings)
						bookIdx := menuIndex - 1
						if bookIdx >= 0 && bookIdx < len(menuBooks) {
							mpv.LoadFile(menuBooks[bookIdx].FilePath)
							inMenu = false
							publishMenu()
						}
					}
				} else {
					scrubMode = !scrubMode
					gui.SetScrubMode(scrubMode)
				}
			case "screen-timeout":
				screenOff = true
				display.SetBacklight(false)
			case bus.EventButtonSkipFwd:
				log.Println("Received EventButtonSkipFwd")
				if resetScreen(true) {
					continue
				}
				if inMenu {
					if menuIndex > 0 {
						menuIndex--
						publishMenu()
					}
				} else {
					mpv.SkipChapter(1)
				}
			case bus.EventButtonSkipBack:
				log.Println("Received EventButtonSkipBack")
				if resetScreen(true) {
					continue
				}
				if inMenu {
					if menuIndex < len(menuBooks) {
						menuIndex++
						publishMenu()
					}
				} else {
					mpv.SkipChapter(-1)
				}
			case bus.EventButtonMenu:
				log.Println("Received EventButtonMenu")
				if resetScreen(true) {
					continue
				}
				inMenu = !inMenu
				if inMenu {
					menuBooks, _ = lib.GetAll()

					// Find the currently playing book to set the cursor
					currentPath := mpv.State.FilePath
					menuIndex = 1 // 0 is settings
					for i, b := range menuBooks {
						if filepath.Base(b.FilePath) == filepath.Base(currentPath) {
							menuIndex = i + 1
							break
						}
					}
				}
				publishMenu()
			case bus.EventEncoderBtn:
				now := time.Now()
				if now.Sub(lastEncoderBtnTime) < 400*time.Millisecond {
					// Double click!
					if singleClickTimer != nil {
						singleClickTimer.Stop()
					}

					if screenOff {
						// Already off, double-click turns it ON!
						resetScreen(true)
					} else {
						// It's on, double-click turns it OFF!
						screenOff = true
						display.SetBacklight(false)
						if screenTimer != nil {
							screenTimer.Stop()
						}
					}
					lastEncoderBtnTime = time.Time{} // reset
				} else {
					// Schedule single click
					resetScreen(false) // Reset timer, don't wake
					singleClickTimer = time.AfterFunc(400*time.Millisecond, func() {
						eventBus.Publish("encoder-single-click", nil)
					})
					lastEncoderBtnTime = now
				}
			case bus.EventEncoderTurn:
				resetScreen(false) // Reset timer, don't wake
				delta, ok := ev.Payload.(int)
				if ok {
					if inMenu && !screenOff {
						// Scroll menu!
						if delta > 0 && menuIndex < len(menuBooks) {
							menuIndex++
							publishMenu()
						} else if delta < 0 && menuIndex > 0 {
							menuIndex--
							publishMenu()
						}
					} else {
						if scrubMode {
							// Scrub by 15 seconds per click
							mpv.Seek(float64(delta * 15))
						} else {
							// Adjust Volume!
							newVol := mpv.State.Volume + float64(delta*5)
							mpv.SetVolume(newVol)
						}
					}
				}
			case bus.EventLibraryScanComplete:
				log.Println("Received EventLibraryScanComplete")
				// We no longer auto-play the first book on scan complete!
			}
		}
	}()

	// Trigger a periodic background scan of audiobooks
	go func() {
		for {
			lib.Scan()
			time.Sleep(5 * time.Minute)
		}
	}()

	// Notify systemd that the service is ready
	sent, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		log.Printf("Failed to notify systemd: %v", err)
	} else if sent {
		log.Println("Systemd notified of readiness.")
	}

	// Handle systemd watchdog
	interval, err := daemon.SdWatchdogEnabled(false)
	if err == nil && interval > 0 {
		go func() {
			ticker := time.NewTicker(interval / 2)
			defer ticker.Stop()
			for range ticker.C {
				daemon.SdNotify(false, daemon.SdNotifyWatchdog)
			}
		}()
	}

	// Wait for termination signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down Bedside...")
}
