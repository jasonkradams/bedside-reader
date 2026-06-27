package display

import (
	"log"
	"os"
)

const brightnessPath = "/sys/class/backlight/backlight_gpio/brightness"

func SetBacklight(on bool) {
	val := "0"
	if on {
		val = "1"
	}
	err := os.WriteFile(brightnessPath, []byte(val), 0644)
	if err != nil {
		log.Printf("Failed to set backlight: %v", err)
	}
}
