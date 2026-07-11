package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

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

	// 3. Start the audio player and resume playback FIRST — before the SPI panel
	// framebuffer, which probes ~20s into boot. Decoupling audio from the display
	// lets a resumed book start playing as soon as the ALSA card is ready, instead
	// of waiting on the (late) panel to open.
	mpv, err := player.New(eventBus, lib)
	if err != nil {
		log.Fatalf("Failed to start player: %v", err)
	}
	defer mpv.Close()

	// Resume saved playback state.
	sysState, err := lib.GetSystemState()
	if err == nil && sysState.ActiveFile != "" {
		log.Printf("Resuming state: %s (Playing: %v, Timeout: %dm)", sysState.ActiveFile, sysState.Playing, sysState.Timeout)
		mpv.LoadFile(sysState.ActiveFile)
		if !sysState.Playing {
			mpv.TogglePause() // LoadFile automatically plays, so pause if it wasn't playing
		}
	} else {
		log.Println("No saved system state, idling.")
	}

	// 4. Start the UI renderer. Opening the panel framebuffer blocks until the
	// ST7789 has probed over SPI, so this comes up after audio is already going.
	gui, err := ui.New(eventBus, lib)
	if err != nil {
		log.Fatalf("Failed to initialize UI: %v", err)
	}
	defer gui.Close()

	// 5. Start the Input Manager (buttons + encoder).
	if _, err := input.New(eventBus); err != nil {
		log.Printf("Warning: Input manager failed to initialize: %v", err)
	}

	// 6. Start the App Controller, then push the current player state so the
	// just-attached UI renders the resumed book immediately (it wasn't subscribed
	// yet when the resume above fired its state event).
	app := NewApp(eventBus, lib, gui, mpv, sysState)
	go app.Run()
	eventBus.Publish(bus.EventPlayerStateChanged, mpv.State)

	// 7. Watch the audiobook directory and (re)scan on changes — event-driven,
	// not polling. Runs an initial scan, then debounced rescans on uploads.
	go lib.Watch()

	// 8. Notify systemd that the service is ready
	systemd.NotifyReady()

	// 9. Handle systemd watchdog
	systemd.StartWatchdog()

	// Wait for termination signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down Bedside...")
	mpv.PersistNow() // flush position/state so a reboot resumes where we stopped
}
