package input

import "testing"

// ab is one sampled level of the two quadrature channels.
type ab struct{ a, b bool }

// feed runs the whole level sequence through a fresh decoder and returns the
// sum of the emitted steps along with how many nonzero steps were emitted.
func feed(seq []ab) (sum, emits int) {
	d := &quadDecoder{}
	for _, s := range seq {
		if step := d.step(s.a, s.b); step != 0 {
			sum += step
			emits++
		}
	}
	return sum, emits
}

// Detent level sequences, both starting and ending at the resting state where
// both channels are high. Clockwise drops A first; counter-clockwise drops B
// first. These are the two valid Gray-code walks for a full-step encoder.
var (
	cwDetent = []ab{
		{true, true},   // rest
		{false, true},  // A falls (B high) -> clockwise per the old decoder
		{false, false}, // B falls
		{true, false},  // A rises
		{true, true},   // B rises, back to rest
	}
	ccwDetent = []ab{
		{true, true},   // rest
		{true, false},  // B falls first
		{false, false}, // A falls
		{false, true},  // B rises
		{true, true},   // A rises, back to rest
	}
)

func TestDecoderOneStepPerDetent(t *testing.T) {
	// A clockwise detent (A leading) must be exactly one +1, matching the
	// original decoder's "A falling while B is high" convention.
	if sum, emits := feed(cwDetent); sum != 1 || emits != 1 {
		t.Errorf("clockwise detent: sum=%d emits=%d, want sum=1 emits=1", sum, emits)
	}
	if sum, emits := feed(ccwDetent); sum != -1 || emits != 1 {
		t.Errorf("counter-clockwise detent: sum=%d emits=%d, want sum=-1 emits=1", sum, emits)
	}
}

func TestDecoderIgnoresBounce(t *testing.T) {
	// Contact bounce replays edges mid-detent: A rattles between low and high
	// before the turn completes. The decoder must still emit exactly one step,
	// in the right direction — this is the bug behind volume jumping several
	// steps per click.
	bouncyCW := []ab{
		{true, true},
		{false, true}, // A falls
		{true, true},  // bounce back
		{false, true}, // A falls again
		{true, true},  // bounce back
		{false, true}, // and settles low
		{false, false},
		{true, false},
		{false, false}, // bounce on the far side too
		{true, false},
		{true, true},
	}
	if sum, emits := feed(bouncyCW); sum != 1 || emits != 1 {
		t.Errorf("bouncy clockwise detent: sum=%d emits=%d, want sum=1 emits=1", sum, emits)
	}
}

func TestDecoderPartialTurnEmitsNothing(t *testing.T) {
	// Rocking the knob short of a detent (A dips and returns without ever
	// completing the sequence) must not emit a phantom step.
	partial := []ab{
		{true, true},
		{false, true},
		{false, false},
		{false, true},
		{true, true},
	}
	if sum, emits := feed(partial); sum != 0 || emits != 0 {
		t.Errorf("partial turn: sum=%d emits=%d, want sum=0 emits=0", sum, emits)
	}
}

func TestDecoderManyDetents(t *testing.T) {
	// Ten clean detents in a row yield exactly ten steps: no catch-up, no drift.
	var seq []ab
	for i := 0; i < 10; i++ {
		seq = append(seq, cwDetent...)
	}
	if sum, emits := feed(seq); sum != 10 || emits != 10 {
		t.Errorf("ten detents: sum=%d emits=%d, want sum=10 emits=10", sum, emits)
	}
}
