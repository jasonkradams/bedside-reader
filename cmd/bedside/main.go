package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/input"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
	"github.com/jasonkradams/bedside-reader/internal/systemd"
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

	// 6. Start the App Controller
	app := NewApp(eventBus, lib, gui, mpv, screenTimeoutMins)
	go app.Run()

	// 7. Trigger a periodic background scan of audiobooks
	go func() {
		for {
			lib.Scan()
			time.Sleep(5 * time.Minute)
		}
	}()

	// 8. Notify systemd that the service is ready
	systemd.NotifyReady()

	// 9. Handle systemd watchdog
	systemd.StartWatchdog()

	// Wait for termination signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down Bedside...")
}
