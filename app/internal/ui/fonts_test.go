package ui

import (
	"strings"
	"testing"

	"golang.org/x/image/font/basicfont"
)

func TestNextFontID(t *testing.T) {
	seen := map[string]bool{}
	id := fontRegistry[0].ID
	for range fontRegistry {
		seen[id] = true
		id = NextFontID(id)
	}
	if len(seen) != len(fontRegistry) {
		t.Errorf("NextFontID visited %d of %d fonts", len(seen), len(fontRegistry))
	}
	if id != fontRegistry[0].ID {
		t.Errorf("NextFontID did not wrap to the first font; got %q", id)
	}
	if got := NextFontID("does-not-exist"); got != defaultFontID {
		t.Errorf("NextFontID(unknown) = %q, want default %q", got, defaultFontID)
	}
}

func TestFontByID(t *testing.T) {
	want := fontRegistry[len(fontRegistry)-1]
	if got := fontByID(want.ID); got.ID != want.ID {
		t.Errorf("fontByID(%q) = %q", want.ID, got.ID)
	}
	if got := fontByID("nope"); got.ID != defaultFontID {
		t.Errorf("fontByID(unknown) = %q, want default", got.ID)
	}
	if got := fontByID(""); got.ID != defaultFontID {
		t.Errorf("fontByID(empty) = %q, want default", got.ID)
	}
}

func TestEllipsize(t *testing.T) {
	face := basicfont.Face7x13
	if got := ellipsize(face, "short", 1000); got != "short" {
		t.Errorf("fitting string was altered: %q", got)
	}
	got := ellipsize(face, "a very long string that clearly overflows the box", 60)
	if textWidth(face, got) > 60 {
		t.Errorf("ellipsized %q width %d exceeds 60", got, textWidth(face, got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected an ellipsis suffix, got %q", got)
	}
}

func TestWrapLines(t *testing.T) {
	face := basicfont.Face7x13
	lines := wrapLines(face, "alpha beta gamma delta epsilon zeta", 60, 2)
	if len(lines) == 0 || len(lines) > 2 {
		t.Fatalf("got %d lines, want 1..2: %v", len(lines), lines)
	}
	for _, ln := range lines {
		if textWidth(face, ln) > 60 {
			t.Errorf("wrapped line %q width %d exceeds 60", ln, textWidth(face, ln))
		}
	}
	// A single word wider than the box is placed alone and ellipsized to fit.
	long := wrapLines(face, "supercalifragilisticexpialidocious", 40, 2)
	if len(long) != 1 || textWidth(face, long[0]) > 40 {
		t.Errorf("long single word not fit to one ellipsized line: %v", long)
	}
}
