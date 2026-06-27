package input

import (
	"log"
	"time"

	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/host/v3"

	"github.com/jasonkradams/bedside-reader/internal/bus"
)

type InputManager struct {
	bus *bus.Bus
}

func New(eventBus *bus.Bus) (*InputManager, error) {
	if _, err := host.Init(); err != nil {
		return nil, err
	}

	m := &InputManager{
		bus: eventBus,
	}

	// Map the Display HAT Mini buttons
	buttons := []struct {
		Name  string
		Pin   string
		Event bus.EventType
	}{
		{"A", "GPIO5", bus.EventButtonPlayPause},
		{"B", "GPIO6", bus.EventButtonMenu},
		{"X", "GPIO16", bus.EventButtonSkipBack},
		{"Y", "GPIO24", bus.EventButtonSkipFwd},
	}

	for _, b := range buttons {
		pin := gpioreg.ByName(b.Pin)
		if pin == nil {
			log.Printf("Warning: Failed to find pin %s", b.Pin)
			continue
		}
		if err := pin.In(gpio.PullUp, gpio.FallingEdge); err != nil {
			log.Printf("Warning: Failed to set pin %s to input: %v", b.Pin, err)
			continue
		}
		go m.watchButton(pin, b.Event)
	}

	// Note: We skip the rotary encoder (GPIO 17, 22, 23) for now 
	// until the hardware is wired up with a breadboard.

	return m, nil
}

func (m *InputManager) watchButton(pin gpio.PinIO, event bus.EventType) {
	for {
		// WaitForEdge blocks until an edge is detected
		if pin.WaitForEdge(-1) {
			// Extremely simple debounce: just sleep for 200ms after a press
			m.bus.Publish(event, nil)
			time.Sleep(200 * time.Millisecond)
		}
	}
}
