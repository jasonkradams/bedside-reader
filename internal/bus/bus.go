package bus

// EventType defines the kind of event being broadcast
type EventType string

const (
	// Input Events
	EventButtonPlayPause EventType = "input.button.play_pause"
	EventButtonMenu      EventType = "input.button.menu"
	EventButtonSkipFwd   EventType = "input.button.skip_fwd"
	EventButtonSkipBack  EventType = "input.button.skip_back"

	// Player Events
	EventPlayerStateChanged   EventType = "player.state_changed"
	EventPlayerProgressTick   EventType = "player.progress_tick"
	EventPlayerChapterChanged EventType = "player.chapter_changed"

	// Library Events
	EventLibraryScanStarted  EventType = "library.scan.started"
	EventLibraryScanComplete EventType = "library.scan.complete"

	// UI Events
	EventMenuUpdate EventType = "ui.menu.update"
)

// MenuState holds the current state of the book selection menu
type MenuState struct {
	Active bool
	Books  any // Will hold []library.Audiobook
	Index  int
}

// Event is the generic wrapper for all messages on the bus
type Event struct {
	Type    EventType
	Payload any
}

// Bus is the central nervous system of the appliance
type Bus struct {
	subscribers []chan Event
	events      chan Event
}

func New() *Bus {
	b := &Bus{
		subscribers: make([]chan Event, 0),
		events:      make(chan Event, 100),
	}
	go b.run()
	return b
}

// Subscribe returns a channel that will receive all events on the bus
func (b *Bus) Subscribe() <-chan Event {
	ch := make(chan Event, 10)
	b.subscribers = append(b.subscribers, ch)
	return ch
}

// Publish broadcasts an event to all subscribers
func (b *Bus) Publish(eventType EventType, payload any) {
	b.events <- Event{
		Type:    eventType,
		Payload: payload,
	}
}

// run is the internal routing loop
func (b *Bus) run() {
	for event := range b.events {
		for _, sub := range b.subscribers {
			// Non-blocking send to prevent one slow component from halting the system
			select {
			case sub <- event:
			default:
			}
		}
	}
}
