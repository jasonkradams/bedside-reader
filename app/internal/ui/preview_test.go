package ui

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
)

// TestGenPreview renders a representative player screen to the PNG named by the
// UI_PREVIEW env var, for local layout review without deploying to hardware.
// It's a dev aid, not an assertion. Run: UI_PREVIEW=/path/out.png go test ./internal/ui -run GenPreview
func TestGenPreview(t *testing.T) {
	out := os.Getenv("UI_PREVIEW")
	if out == "" {
		t.Skip("set UI_PREVIEW=<path> to generate the preview PNG")
	}

	r := &Renderer{
		canvas:        image.NewRGBA(image.Rect(0, 0, panelWidth, panelHeight)),
		fonts:         newFontManager(),
		fontChoice:    fontByID(defaultFontID),
		encoderMode:   "vol",
		wifiConnected: true,
		playState: player.PlaybackState{
			FilePath: "HP3.m4b", Paused: false, Volume: 65,
			Position: 5*3600 + 45*60 + 8, Duration: 11*3600 + 49*60,
		},
	}
	book := &library.Audiobook{
		ID: "x", CoverHash: "x",
		Title:    "Harry Potter and the Prisoner of Azkaban, Book 3",
		Author:   "J.K. Rowling",
		Duration: 11*3600 + 49*60,
		Chapters: []library.Chapter{{StartTime: 0}, {StartTime: 20400}, {StartTime: 22200}},
	}

	// Fake cover so the layout looks realistic without loading from disk.
	cov := image.NewRGBA(image.Rect(0, 0, coverSize, coverSize))
	for y := 0; y < coverSize; y++ {
		for x := 0; x < coverSize; x++ {
			cov.Set(x, y, color.RGBA{uint8(60 + x/3), uint8(30 + y/4), 130, 255})
		}
	}
	r.coverID = "x"
	r.coverImg = cov

	draw.Draw(r.canvas, r.canvas.Bounds(), &image.Uniform{colorBg}, image.Point{}, draw.Src)
	r.drawPlayer(book, r.resolveChapter(book), r.displayTitle(book))
	r.drawWiFiIcon(panelWidth-pad-10, statusY-9, r.wifiConnected)

	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, r.canvas); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}
