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
	m.setupButtons()
	m.setupEncoder()

	return m, nil
}

// setupButtons wires the Display HAT Mini buttons. Each watcher blocks on a
// falling edge (press) via the GPIO chardev instead of polling, so idle input
// costs no CPU. Missing or unconfigurable pins are logged and skipped.
func (m *InputManager) setupButtons() {
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
}

// setupEncoder wires the rotary encoder (GPIO 4, 20, 23). A is watched on both
// edges for quadrature; the push button waits on a falling edge. If any of the
// three pins is missing, the encoder is skipped.
func (m *InputManager) setupEncoder() {
	pinA := gpioreg.ByName("GPIO4")
	pinB := gpioreg.ByName("GPIO20")
	pinBtn := gpioreg.ByName("GPIO23")

	if pinA == nil || pinB == nil || pinBtn == nil {
		log.Println("Warning: Failed to find Rotary Encoder pins")
		return
	}

	if err := pinA.In(gpio.PullUp, gpio.BothEdges); err != nil {
		log.Printf("Warning: encoder pin A: %v", err)
		return
	}
	if err := pinB.In(gpio.PullUp, gpio.NoEdge); err != nil {
		log.Printf("Warning: encoder pin B: %v", err)
		return
	}
	if err := pinBtn.In(gpio.PullUp, gpio.FallingEdge); err != nil {
		log.Printf("Warning: encoder button: %v", err)
		return
	}

	go m.watchEncoder(pinA, pinB)
	go m.watchButton(pinBtn, bus.EventEncoderBtn)
}

// buttonDebounce drops edges closer together than this (contact bounce), while
// still allowing fast double-clicks.
const buttonDebounce = 60 * time.Millisecond

// watchButton blocks until a falling edge (press) and publishes event.
func (m *InputManager) watchButton(pin gpio.PinIO, event bus.EventType) {
	var last time.Time
	for {
		if !pin.WaitForEdge(-1) {
			time.Sleep(10 * time.Millisecond) // guard against a hot loop if edges error
			continue
		}
		if pin.Read() != gpio.Low {
			continue // released before we read
		}
		now := time.Now()
		if now.Sub(last) < buttonDebounce {
			continue
		}
		last = now
		m.bus.Publish(event, nil)
	}
}

// watchEncoder decodes quadrature: on A's falling edge, B's level gives the
// direction.
func (m *InputManager) watchEncoder(pinA, pinB gpio.PinIO) {
	for {
		if !pinA.WaitForEdge(-1) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if pinA.Read() != gpio.Low {
			continue // act on the falling edge only
		}
		if pinB.Read() == gpio.High {
			m.bus.Publish(bus.EventEncoderTurn, 1) // clockwise
		} else {
			m.bus.Publish(bus.EventEncoderTurn, -1) // counter-clockwise
		}
	}
}
