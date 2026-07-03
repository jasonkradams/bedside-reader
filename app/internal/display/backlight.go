package display

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// We must calculate the GPIO base offset dynamically
	gpioNum := "13"
	if matches, err := filepath.Glob("/sys/class/gpio/gpiochip*/base"); err == nil && len(matches) > 0 {
		if content, err := os.ReadFile(matches[0]); err == nil {
			if base, err := strconv.Atoi(strings.TrimSpace(string(content))); err == nil {
				gpioNum = strconv.Itoa(base + 13)
			}
		}
	}

	gpioPath := "/sys/class/gpio/gpio" + gpioNum

	if _, err := os.Stat(gpioPath); os.IsNotExist(err) {
		// Export the GPIO
		if err := os.WriteFile("/sys/class/gpio/export", []byte(gpioNum), 0200); err == nil {
			// Set direction to out
			os.WriteFile(gpioPath+"/direction", []byte("out"), 0644)
		} else {
			log.Printf("Failed to export GPIO %s: %v", gpioNum, err)
			return
		}
	}
	
	if err := os.WriteFile(gpioPath+"/value", []byte(val), 0644); err != nil {
		log.Printf("Failed to set GPIO %s backlight: %v", gpioNum, err)
	}
}
