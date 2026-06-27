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
		if err := pin.In(gpio.PullUp, gpio.NoEdge); err != nil {
			log.Printf("Warning: Failed to set pin %s to input: %v", b.Pin, err)
			continue
		}
		go m.watchButton(pin, b.Event)
	}

	// Start Rotary Encoder (GPIO 17, 22, 23)
	pinA := gpioreg.ByName("GPIO17")
	pinB := gpioreg.ByName("GPIO22")
	pinBtn := gpioreg.ByName("GPIO23")

	if pinA != nil && pinB != nil && pinBtn != nil {
		pinA.In(gpio.PullUp, gpio.NoEdge)
		pinB.In(gpio.PullUp, gpio.NoEdge)
		pinBtn.In(gpio.PullUp, gpio.NoEdge)

		go m.watchEncoder(pinA, pinB)
		go m.watchButton(pinBtn, bus.EventEncoderBtn)
	} else {
		log.Println("Warning: Failed to find Rotary Encoder pins")
	}

	return m, nil
}

func (m *InputManager) watchEncoder(pinA, pinB gpio.PinIO) {
	// Simple quadrature decoder using polling
	lastA := pinA.Read()
	lastB := pinB.Read()

	for {
		time.Sleep(2 * time.Millisecond) // Fast 2ms polling loop for accurate quadrature

		a := pinA.Read()
		b := pinB.Read()

		if a != lastA || b != lastB {
			// If state changed
			if a == gpio.Low && lastA == gpio.High {
				// Falling edge on A
				if b == gpio.High {
					// Clockwise
					m.bus.Publish(bus.EventEncoderTurn, 1)
				} else {
					// Counter-clockwise
					m.bus.Publish(bus.EventEncoderTurn, -1)
				}
			}
			lastA = a
			lastB = b
		}
	}
}

func (m *InputManager) watchButton(pin gpio.PinIO, event bus.EventType) {
	wasPressed := false
	for {
		isPressed := pin.Read() == gpio.Low
		if isPressed && !wasPressed {
			m.bus.Publish(event, nil)
			time.Sleep(200 * time.Millisecond) // debounce
		}
		wasPressed = isPressed
		time.Sleep(20 * time.Millisecond) // poll every 20ms
	}
}
