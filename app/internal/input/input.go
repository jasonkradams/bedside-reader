package input

import (
	"log"
	"sync"
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

// setupEncoder wires the rotary encoder (GPIO 4, 20, 23). Both quadrature pins
// are watched on both edges so the decoder sees every transition; the push
// button waits on a falling edge. If any of the three pins is missing, the
// encoder is skipped.
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
	if err := pinB.In(gpio.PullUp, gpio.BothEdges); err != nil {
		log.Printf("Warning: encoder pin B: %v", err)
		return
	}
	if err := pinBtn.In(gpio.PullUp, gpio.FallingEdge); err != nil {
		log.Printf("Warning: encoder button: %v", err)
		return
	}

	m.watchEncoder(pinA, pinB)
	go m.watchButton(pinBtn, bus.EventEncoderBtn)
}

// buttonDebounce drops edges closer together than this (contact bounce), while
// still allowing fast double-clicks.
const buttonDebounce = 60 * time.Millisecond

// watchButton blocks until a falling edge (press) and publishes event.
//
// The timeout is 0, not -1: the gpioioctl driver treats the value as a read
// deadline, so 0 means "wait forever" while a negative value times out at once.
func (m *InputManager) watchButton(pin gpio.PinIO, event bus.EventType) {
	var last time.Time
	for {
		if !pin.WaitForEdge(0) {
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

// watchEncoder decodes the quadrature encoder. Both pins feed a shared
// quadDecoder guarded by a mutex; a goroutine per pin wakes on every edge and
// advances the state machine off the pins' live levels. The decoder emits at
// most one step per detent and swallows contact bounce, so a single click of
// the knob is exactly one turn event.
func (m *InputManager) watchEncoder(pinA, pinB gpio.PinIO) {
	d := &quadDecoder{}
	watch := func(pin gpio.PinIO) {
		for {
			if !pin.WaitForEdge(0) { // 0 = wait forever (see watchButton)
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if step := d.step(pinA.Read() == gpio.High, pinB.Read() == gpio.High); step != 0 {
				m.bus.Publish(bus.EventEncoderTurn, step)
			}
		}
	}
	go watch(pinA)
	go watch(pinB)
}

// Ben Buxton's rotary-encoder state table (full step). Each detent walks the
// state machine through a fixed sequence of the two pins' Gray-coded levels;
// only a complete, valid sequence yields a direction, which is what makes it
// immune to the mid-turn bounce that a naive edge-count decoder miscounts.
//
// The table is indexed by the current state and the 2-bit pin level
// (bit 0 = B, bit 1 = A). A resolved step is carried in the high bits.
const (
	encStart    = 0x0
	encCWFinal  = 0x1
	encCWBegin  = 0x2
	encCWNext   = 0x3
	encCCWBegin = 0x4
	encCCWFinal = 0x5
	encCCWNext  = 0x6

	dirCW   = 0x10 // clockwise step resolved this transition
	dirCCW  = 0x20 // counter-clockwise step resolved this transition
	dirMask = 0x30
)

var quadTable = [7][4]uint8{
	encStart:    {encStart, encCWBegin, encCCWBegin, encStart},
	encCWFinal:  {encCWNext, encStart, encCWFinal, encStart | dirCW},
	encCWBegin:  {encCWNext, encCWBegin, encStart, encStart},
	encCWNext:   {encCWNext, encCWBegin, encCWFinal, encStart},
	encCCWBegin: {encCCWNext, encStart, encCCWBegin, encStart},
	encCCWFinal: {encCCWNext, encCCWFinal, encStart, encStart | dirCCW},
	encCCWNext:  {encCCWNext, encCCWFinal, encCCWBegin, encStart},
}

// quadDecoder holds the running state of the encoder decode. It is not safe for
// concurrent use; watchEncoder serializes calls through the mutex.
type quadDecoder struct {
	mu    sync.Mutex
	state uint8
}

// step advances the decoder with the pins' current levels and returns +1 for a
// clockwise detent, -1 for counter-clockwise, or 0 for an intermediate or
// bouncing transition that hasn't completed a detent. Clockwise (A falling
// while B is high) maps to +1 to match the previous decoder's direction.
func (d *quadDecoder) step(a, b bool) int {
	d.mu.Lock()
	defer d.mu.Unlock()

	pinstate := uint8(0)
	if b {
		pinstate |= 1
	}
	if a {
		pinstate |= 2
	}
	d.state = quadTable[d.state&0x0f][pinstate]
	switch d.state & dirMask {
	case dirCW:
		return 1
	case dirCCW:
		return -1
	default:
		return 0
	}
}
