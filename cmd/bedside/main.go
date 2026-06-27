package main

import (
	"log"
	"os"
	"os/signal"
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

	// 6. Connect Input to Player logic
	ch := eventBus.Subscribe()
	go func() {
		for ev := range ch {
			switch ev.Type {
			case bus.EventButtonPlayPause:
				log.Println("Received EventButtonPlayPause")
				mpv.TogglePause()
			case bus.EventButtonSkipFwd:
				log.Println("Received EventButtonSkipFwd")
				mpv.SkipChapter(1)
			case bus.EventButtonSkipBack:
				log.Println("Received EventButtonSkipBack")
				mpv.SkipChapter(-1)
			case bus.EventButtonMenu:
				log.Println("Received EventButtonMenu (Not Implemented)")
			case bus.EventLibraryScanComplete:
				log.Println("Received EventLibraryScanComplete")
				books, _ := lib.GetAll()
				if len(books) > 0 {
					mpv.LoadFile(books[0].FilePath)
					// Automatically pause it initially so it doesn't start blasting
					mpv.TogglePause()
				}
			}
		}
	}()

	// Trigger a background scan of audiobooks
	go lib.Scan()

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
