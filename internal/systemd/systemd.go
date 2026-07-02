package systemd

import (
	"log"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
)

// NotifyReady signals systemd that the application has finished initialization
// and is ready to serve requests or perform its duties.
func NotifyReady() {
	sent, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		log.Printf("Failed to notify systemd: %v", err)
	} else if sent {
		log.Println("Systemd notified of readiness.")
	}
}

// StartWatchdog checks if systemd watchdog is enabled for this service,
// and if so, spawns a background goroutine to periodically ping systemd
// to prove the process is still healthy.
func StartWatchdog() {
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
}
