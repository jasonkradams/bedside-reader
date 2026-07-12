package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
)

// Golden-image regression tests for the on-screen layout. They render
// representative states to an off-screen canvas (no framebuffer needed) and
// compare against committed PNGs in testdata/golden. Regenerate after an
// intentional layout change with: UPDATE_GOLDEN=1 go test ./internal/ui
//
// Comparison tolerates a few differing pixels so minor font-rasterization jitter
// across Go toolchains doesn't cause false failures; a real layout change moves
// far more than the threshold.
const (
	goldenDir      = "testdata/golden"
	goldenChanTol  = 24  // per-pixel channel-sum difference that counts as "changed"
	goldenPixelTol = 120 // max changed pixels before the render is considered regressed
)

var hp3 = &library.Audiobook{
	ID: "hp3", CoverHash: "hp3",
	Title:    "Harry Potter and the Prisoner of Azkaban, Book 3",
	Author:   "J.K. Rowling",
	Duration: 11*3600 + 49*60,
	Chapters: []library.Chapter{{StartTime: 0}, {StartTime: 20400}, {StartTime: 22200}},
}

type goldenCase struct {
	name  string
	setup func(r *Renderer) (book *library.Audiobook, title string)
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{"playing", func(r *Renderer) (*library.Audiobook, string) {
			r.wifiConnected = true
			r.playState = player.PlaybackState{FilePath: "hp3.m4b", Volume: 65, Position: 20708, Duration: hp3.Duration}
			r.coverID, r.coverImg = hp3.ID, syntheticCover()
			return hp3, hp3.Title
		}},
		{"paused", func(r *Renderer) (*library.Audiobook, string) {
			r.wifiConnected = true
			r.playState = player.PlaybackState{FilePath: "hp3.m4b", Paused: true, Volume: 65, Position: 20708, Duration: hp3.Duration}
			r.coverID, r.coverImg = hp3.ID, syntheticCover()
			return hp3, hp3.Title
		}},
		{"idle", func(r *Renderer) (*library.Audiobook, string) {
			return nil, idleTitle
		}},
		{"named_chapter_scrub", func(r *Renderer) (*library.Audiobook, string) {
			b := &library.Audiobook{
				ID: "hob", CoverHash: "hob", Title: "The Hobbit", Author: "J.R.R. Tolkien", Duration: 39600,
				Chapters: []library.Chapter{{Title: "An Unexpected Party", StartTime: 0}, {Title: "Roast Mutton", StartTime: 1800}},
			}
			r.encoderMode = "scrub"
			r.playState = player.PlaybackState{FilePath: "hob.m4b", Volume: 40, Position: 600, Duration: b.Duration}
			r.coverID, r.coverImg = b.ID, syntheticCover()
			return b, b.Title
		}},
		{"menu", func(r *Renderer) (*library.Audiobook, string) {
			r.wifiConnected = true
			r.playState = player.PlaybackState{FilePath: "hp3.m4b", Volume: 65, Position: 20708, Duration: hp3.Duration}
			r.coverID, r.coverImg = hp3.ID, syntheticCover()
			r.menuState = bus.MenuState{Active: true, Index: 2, Books: []library.Audiobook{
				{Title: "Harry Potter and the Prisoner of Azkaban", FilePath: "hp3.m4b"},
				{Title: "The Hobbit", FilePath: "hob.m4b"},
				{Title: "Dune", FilePath: "dune.m4b"},
			}}
			return hp3, hp3.Title
		}},
	}
}

func TestGoldenRender(t *testing.T) {
	update := os.Getenv("UPDATE_GOLDEN") != ""
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRenderer(t)
			r.compose(tc.setup(r))

			path := filepath.Join(goldenDir, tc.name+".png")
			if update {
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				var buf bytes.Buffer
				if err := png.Encode(&buf, r.canvas); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("updated %s", path)
				return
			}

			want, err := loadPNG(path)
			if err != nil {
				t.Fatalf("read golden (run UPDATE_GOLDEN=1 to create): %v", err)
			}
			if n := imgDiff(r.canvas, want); n > goldenPixelTol {
				actual := filepath.Join(goldenDir, tc.name+".actual.png")
				writePNG(actual, r.canvas)
				t.Errorf("%s: %d pixels differ (tol %d); wrote %s. If intended, run UPDATE_GOLDEN=1 go test ./internal/ui",
					tc.name, n, goldenPixelTol, actual)
			}
		})
	}
}

// TestLayoutFillsScreen guards the "use the whole screen" invariant: content
// must reach near every edge, so a future change can't quietly reintroduce a
// blank strip. Platform-independent (no pixel-exact comparison).
func TestLayoutFillsScreen(t *testing.T) {
	r := newTestRenderer(t)
	r.wifiConnected = true
	r.playState = player.PlaybackState{FilePath: "hp3.m4b", Volume: 65, Position: 20708, Duration: hp3.Duration}
	r.coverID, r.coverImg = hp3.ID, syntheticCover()
	r.compose(hp3, hp3.Title)

	lo, hi := contentBounds(r.canvas)
	checks := []struct {
		name string
		got  int
		op   string
		want int
	}{
		{"left edge reached", lo.X, "<=", coverX + 2},
		{"top edge reached", lo.Y, "<=", coverY + 6},
		{"right edge reached", hi.X, ">=", panelWidth - pad - 6},
		{"bottom edge reached", hi.Y, ">=", statusY - 6},
	}
	for _, c := range checks {
		bad := (c.op == "<=" && c.got > c.want) || (c.op == ">=" && c.got < c.want)
		if bad {
			t.Errorf("%s: content bound %d, want %s %d (blank edge)", c.name, c.got, c.op, c.want)
		}
	}
}

// --- helpers ---

// compose renders one full frame the way Renderer.render does, but with the book
// and title injected so no on-disk library entry is required.
func (r *Renderer) compose(book *library.Audiobook, title string) {
	draw.Draw(r.canvas, r.canvas.Bounds(), &image.Uniform{colorBg}, image.Point{}, draw.Src)
	r.drawPlayer(book, r.resolveChapter(book), title)
	if r.menuState.Active {
		r.renderMenu()
	}
	r.drawWiFiIcon(panelWidth-pad-10, statusY-9, r.wifiConnected)
}

func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	dir := t.TempDir()
	lib, err := library.New(bus.New(), filepath.Join(dir, "lib.db"), filepath.Join(dir, "audio"), filepath.Join(dir, "covers"))
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	t.Cleanup(lib.Close)
	return &Renderer{
		canvas:      image.NewRGBA(image.Rect(0, 0, panelWidth, panelHeight)),
		fonts:       newFontManager(),
		fontChoice:  fontByID(defaultFontID),
		lib:         lib,
		encoderMode: "vol",
	}
}

// syntheticCover is a deterministic cover so golden renders are reproducible.
func syntheticCover() *image.RGBA {
	c := image.NewRGBA(image.Rect(0, 0, coverSize, coverSize))
	for y := 0; y < coverSize; y++ {
		for x := 0; x < coverSize; x++ {
			c.Set(x, y, color.RGBA{uint8(60 + x/3), uint8(30 + y/4), 130, 255})
		}
	}
	return c
}

// contentBounds returns the bounding box of non-background pixels.
func contentBounds(img *image.RGBA) (lo, hi image.Point) {
	lo = image.Point{img.Bounds().Dx(), img.Bounds().Dy()}
	hi = image.Point{-1, -1}
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.RGBAAt(x, y) == colorBg {
				continue
			}
			lo.X, lo.Y = min(lo.X, x), min(lo.Y, y)
			hi.X, hi.Y = max(hi.X, x), max(hi.Y, y)
		}
	}
	return lo, hi
}

// imgDiff counts pixels whose channel-sum differs by more than goldenChanTol.
func imgDiff(a, b *image.RGBA) int {
	n := 0
	for i := 0; i+3 < len(a.Pix) && i+3 < len(b.Pix); i += 4 {
		d := absDiff(a.Pix[i], b.Pix[i]) + absDiff(a.Pix[i+1], b.Pix[i+1]) + absDiff(a.Pix[i+2], b.Pix[i+2])
		if d > goldenChanTol {
			n++
		}
	}
	return n
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

func loadPNG(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst, nil
}

func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = png.Encode(f, img)
}
