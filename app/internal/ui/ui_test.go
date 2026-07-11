package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
)

func TestPickPanelFB(t *testing.T) {
	t.Run("prefers panel driver name over HDMI framebuffers", func(t *testing.T) {
		fbs := []fbInfo{
			{dev: "/dev/fb0", name: "BCM2708 FB", geometry: "720,480"},
			{dev: "/dev/fb1", name: "simpledrmdrmfb", geometry: "720,480"},
			{dev: "/dev/fb2", name: "panel-mipi-dbid", geometry: "320,240"},
		}
		if got, ok := pickPanelFB(fbs); !ok || got != "/dev/fb2" {
			t.Errorf("pickPanelFB = %q, %v; want /dev/fb2, true", got, ok)
		}
	})

	t.Run("falls back to 320x240 geometry when no name matches", func(t *testing.T) {
		fbs := []fbInfo{
			{dev: "/dev/fb0", name: "BCM2708 FB", geometry: "720,480"},
			{dev: "/dev/fb1", name: "mysteryfb", geometry: "320,240"},
		}
		if got, ok := pickPanelFB(fbs); !ok || got != "/dev/fb1" {
			t.Errorf("pickPanelFB = %q, %v; want /dev/fb1, true", got, ok)
		}
	})

	t.Run("no panel and no matching geometry returns false", func(t *testing.T) {
		fbs := []fbInfo{
			{dev: "/dev/fb0", name: "BCM2708 FB", geometry: "720,480"},
		}
		if got, ok := pickPanelFB(fbs); ok {
			t.Errorf("pickPanelFB = %q, %v; want \"\", false", got, ok)
		}
	})
}

func TestClamp01(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{-0.5, 0},
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{1.5, 1},
	}
	for _, tc := range cases {
		if got := clamp01(tc.in); got != tc.want {
			t.Errorf("clamp01(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFormatMinSec(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "00:00"},
		{5, "00:05"},
		{65, "01:05"},
		{3599, "59:59"},
	}
	for _, tc := range cases {
		if got := formatMinSec(tc.in); got != tc.want {
			t.Errorf("formatMinSec(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatHourMin(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "00h00m"},
		{59, "00h00m"},
		{60, "00h01m"},
		{3661, "01h01m"},
	}
	for _, tc := range cases {
		if got := formatHourMin(tc.in); got != tc.want {
			t.Errorf("formatHourMin(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFillRect(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	fg := color.RGBA{10, 20, 30, 255}
	fillRect(img, 2, 3, 4, 5, fg) // fills x in [2,6), y in [3,8)

	inside := []image.Point{{2, 3}, {5, 7}}
	for _, p := range inside {
		if got := img.RGBAAt(p.X, p.Y); got != fg {
			t.Errorf("pixel (%d,%d) = %v, want filled %v", p.X, p.Y, got, fg)
		}
	}

	outside := []image.Point{{0, 0}, {6, 3}, {2, 8}}
	for _, p := range outside {
		if got := img.RGBAAt(p.X, p.Y); got != (color.RGBA{}) {
			t.Errorf("pixel (%d,%d) = %v, want empty", p.X, p.Y, got)
		}
	}
}

func TestRenderer_DisplayTitle(t *testing.T) {
	cases := []struct {
		name  string
		state player.PlaybackState
		book  *library.Audiobook
		want  string
	}{
		{"idle when no file", player.PlaybackState{}, nil, idleTitle},
		{"idle ignores book", player.PlaybackState{}, &library.Audiobook{Title: "X"}, idleTitle},
		{"book title preferred", player.PlaybackState{FilePath: "f.m4b"}, &library.Audiobook{Title: "Real"}, "Real"},
		{"falls back to path when book nil", player.PlaybackState{FilePath: "f.m4b"}, nil, "f.m4b"},
		{"falls back to path when title empty", player.PlaybackState{FilePath: "f.m4b"}, &library.Audiobook{Title: ""}, "f.m4b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Renderer{playState: tc.state}
			if got := r.displayTitle(tc.book); got != tc.want {
				t.Errorf("displayTitle() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderer_ResolveChapter(t *testing.T) {
	book := &library.Audiobook{
		Duration: 600,
		Chapters: []library.Chapter{
			{Title: "Ch1", StartTime: 0},
			{Title: "Ch2", StartTime: 100},
			{Title: "Ch3", StartTime: 200},
		},
	}

	t.Run("no book falls back to stream duration", func(t *testing.T) {
		r := &Renderer{playState: player.PlaybackState{Duration: 1234}}
		got := r.resolveChapter(nil)
		want := chapterInfo{title: "", start: 0, end: 1234}
		if got != want {
			t.Errorf("resolveChapter(nil) = %+v, want %+v", got, want)
		}
	})

	t.Run("middle chapter bounded by next chapter", func(t *testing.T) {
		r := &Renderer{playState: player.PlaybackState{Position: 150, Duration: 600}}
		got := r.resolveChapter(book)
		want := chapterInfo{title: "Ch2", start: 100, end: 200}
		if got != want {
			t.Errorf("resolveChapter() = %+v, want %+v", got, want)
		}
	})

	t.Run("last chapter bounded by book duration", func(t *testing.T) {
		r := &Renderer{playState: player.PlaybackState{Position: 250, Duration: 600}}
		got := r.resolveChapter(book)
		want := chapterInfo{title: "Ch3", start: 200, end: 600}
		if got != want {
			t.Errorf("resolveChapter() = %+v, want %+v", got, want)
		}
	})

	t.Run("position before first chapter yields no chapter", func(t *testing.T) {
		early := &library.Audiobook{
			Duration: 600,
			Chapters: []library.Chapter{{Title: "Ch1", StartTime: 10}},
		}
		r := &Renderer{playState: player.PlaybackState{Position: 5, Duration: 600}}
		got := r.resolveChapter(early)
		want := chapterInfo{title: "", start: 0, end: 600}
		if got != want {
			t.Errorf("resolveChapter() = %+v, want %+v", got, want)
		}
	})

	t.Run("zero book duration falls back to stream duration", func(t *testing.T) {
		noDur := &library.Audiobook{Duration: 0}
		r := &Renderer{playState: player.PlaybackState{Position: 50, Duration: 555}}
		got := r.resolveChapter(noDur)
		want := chapterInfo{title: "", start: 0, end: 555}
		if got != want {
			t.Errorf("resolveChapter() = %+v, want %+v", got, want)
		}
	})
}

func TestMenuScrollStart(t *testing.T) {
	cases := []struct {
		index int
		want  int
	}{
		{0, 0},
		{5, 0},
		{6, 1},
		{10, 5},
	}
	for _, tc := range cases {
		if got := menuScrollStart(tc.index); got != tc.want {
			t.Errorf("menuScrollStart(%d) = %d, want %d", tc.index, got, tc.want)
		}
	}
}

func TestBookTitle(t *testing.T) {
	cases := []struct {
		name string
		book library.Audiobook
		want string
	}{
		{"uses title", library.Audiobook{Title: "Dune"}, "Dune"},
		{"falls back to basename", library.Audiobook{FilePath: "/audio/dune.m4b"}, "dune.m4b"},
		{"long title returned as-is (wrapping/ellipsis happen at render)", library.Audiobook{Title: "A Really Very Long Audiobook Title That Overflows"}, "A Really Very Long Audiobook Title That Overflows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bookTitle(tc.book); got != tc.want {
				t.Errorf("bookTitle() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTimeoutLabel(t *testing.T) {
	cases := []struct {
		minutes int
		want    string
	}{
		{0, "Off"},
		{-1, "Off"},
		{5, "5m"},
		{30, "30m"},
	}
	for _, tc := range cases {
		if got := timeoutLabel(tc.minutes); got != tc.want {
			t.Errorf("timeoutLabel(%d) = %q, want %q", tc.minutes, got, tc.want)
		}
	}
}

func TestRenderer_MenuBooks(t *testing.T) {
	books := []library.Audiobook{{Title: "A"}, {Title: "B"}}

	if got := (&Renderer{menuState: bus.MenuState{Books: books}}).menuBooks(); len(got) != 2 {
		t.Errorf("with books: len = %d, want 2", len(got))
	}
	if got := (&Renderer{menuState: bus.MenuState{Books: nil}}).menuBooks(); got != nil {
		t.Errorf("with nil: got %v, want nil", got)
	}
	if got := (&Renderer{menuState: bus.MenuState{Books: "not books"}}).menuBooks(); got != nil {
		t.Errorf("with wrong type: got %v, want nil", got)
	}
}

func TestRenderer_BookRowStyle(t *testing.T) {
	playing := library.Audiobook{FilePath: "/audio/current.m4b"}
	other := library.Audiobook{FilePath: "/audio/other.m4b"}

	t.Run("selected row", func(t *testing.T) {
		// Book row i is selected when menuIndex == i + SettingsRowCount.
		r := &Renderer{menuState: bus.MenuState{Index: 1 + SettingsRowCount}}
		prefix, c := r.bookRowStyle(1, other)
		if prefix != "> " || c != colorText {
			t.Errorf("got (%q, %v), want (\"> \", colorText)", prefix, c)
		}
	})

	t.Run("currently playing row", func(t *testing.T) {
		r := &Renderer{
			menuState: bus.MenuState{Index: 99},
			playState: player.PlaybackState{FilePath: "current.m4b"},
		}
		prefix, c := r.bookRowStyle(0, playing)
		if prefix != "* " || c != colorStatus {
			t.Errorf("got (%q, %v), want (\"* \", colorStatus)", prefix, c)
		}
	})

	t.Run("plain row", func(t *testing.T) {
		r := &Renderer{menuState: bus.MenuState{Index: 99}}
		prefix, c := r.bookRowStyle(0, other)
		if prefix != "  " || c != colorMuted {
			t.Errorf("got (%q, %v), want (\"  \", colorMuted)", prefix, c)
		}
	})

	t.Run("selection wins over playing", func(t *testing.T) {
		r := &Renderer{
			menuState: bus.MenuState{Index: 0 + SettingsRowCount},
			playState: player.PlaybackState{FilePath: "current.m4b"},
		}
		prefix, _ := r.bookRowStyle(0, playing) // selected AND playing
		if prefix != "> " {
			t.Errorf("got %q, want \"> \" (selection precedence)", prefix)
		}
	})
}
