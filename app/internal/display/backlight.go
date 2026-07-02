package display

import (
	"log"
	"os"
	"path/filepath"
)

func SetBacklight(on bool) {
	// Dynamically find the backlight path since it might be named '10-0045' or 'spi0.1' etc.
	matches, err := filepath.Glob("/sys/class/backlight/*/brightness")
	if err != nil || len(matches) == 0 {
		log.Printf("Failed to find backlight brightness file: %v", err)
		return
	}
	
	brightnessPath := matches[0]
	val := "0"
	if on {
		val = "1"
	}
	err = os.WriteFile(brightnessPath, []byte(val), 0644)
	if err != nil {
		log.Printf("Failed to set backlight via %s: %v", brightnessPath, err)
	}
}
