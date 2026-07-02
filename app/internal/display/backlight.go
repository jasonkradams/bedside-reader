package display

import (
	"log"
	"os"
	"path/filepath"
)

func SetBacklight(on bool) {
	val := "0"
	if on {
		val = "1"
	}

	// Dynamically find the backlight path since it might be named '10-0045' or 'spi0.1' etc.
	matches, err := filepath.Glob("/sys/class/backlight/*/brightness")
	if err == nil && len(matches) > 0 {
		err = os.WriteFile(matches[0], []byte(val), 0644)
		if err != nil {
			log.Printf("Failed to set backlight via %s: %v", matches[0], err)
		}
		return
	}

	// Fallback to raw sysfs GPIO 13 if no backlight class device exists
	if _, err := os.Stat("/sys/class/gpio/gpio13"); os.IsNotExist(err) {
		// Export GPIO 13
		if err := os.WriteFile("/sys/class/gpio/export", []byte("13"), 0200); err == nil {
			// Set direction to out
			os.WriteFile("/sys/class/gpio/gpio13/direction", []byte("out"), 0644)
		} else {
			log.Printf("Failed to export GPIO 13: %v", err)
			return
		}
	}
	
	if err := os.WriteFile("/sys/class/gpio/gpio13/value", []byte(val), 0644); err != nil {
		log.Printf("Failed to set GPIO 13 backlight: %v", err)
	}
}
