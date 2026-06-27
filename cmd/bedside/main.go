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
	activePath, playing, err := lib.GetSystemState()
	if err == nil && activePath != "" {
		log.Printf("Resuming state: %s (Playing: %v)", activePath, playing)
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
		inMenu := false
		menuIndex := 0
		var menuBooks []library.Audiobook
		scrubMode := false // Toggle for encoder behavior

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
				if inMenu {
					if len(menuBooks) > 0 && menuIndex < len(menuBooks) {
						mpv.LoadFile(menuBooks[menuIndex].FilePath)
					}
					inMenu = false
					publishMenu()
				} else {
					mpv.TogglePause()
				}
			case bus.EventButtonSkipFwd:
				log.Println("Received EventButtonSkipFwd")
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
				if inMenu {
					if menuIndex < len(menuBooks)-1 {
						menuIndex++
						publishMenu()
					}
				} else {
					mpv.SkipChapter(-1)
				}
			case bus.EventButtonMenu:
				log.Println("Received EventButtonMenu")
				inMenu = !inMenu
				if inMenu {
					menuBooks, _ = lib.GetAll()

					// Find the currently playing book to set the cursor
					currentPath := mpv.State.FilePath
					menuIndex = 0
					for i, b := range menuBooks {
						if filepath.Base(b.FilePath) == currentPath {
							menuIndex = i
							break
						}
					}
				}
				publishMenu()
			case bus.EventEncoderBtn:
				log.Println("Received EventEncoderBtn")
				scrubMode = !scrubMode
				if scrubMode {
					log.Println("Encoder Mode: Scrubbing")
				} else {
					log.Println("Encoder Mode: Volume")
				}
			case bus.EventEncoderTurn:
				delta, ok := ev.Payload.(int)
				if ok {
					if inMenu {
						// Scroll menu!
						if delta > 0 && menuIndex < len(menuBooks)-1 {
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
