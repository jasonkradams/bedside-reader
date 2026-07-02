package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"os"
	"path/filepath"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/unix"
)

type Renderer struct {
	bus    *bus.Bus
	lib    *library.Manager
	fbFile *os.File
	mmap   []byte
	canvas *image.RGBA

	// Local state
	playState player.PlaybackState
	menuState bus.MenuState
	scrubMode bool
}

func New(eventBus *bus.Bus, lib *library.Manager) (*Renderer, error) {
	// Open the framebuffer device
	fbFile, err := os.OpenFile("/dev/fb1", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open /dev/fb1: %w", err)
	}

	// Memory map the framebuffer (320x240, 16-bit color = 153600 bytes)
	fbSize := 320 * 240 * 2
	mmap, err := unix.Mmap(int(fbFile.Fd()), 0, fbSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		fbFile.Close()
		return nil, fmt.Errorf("failed to mmap framebuffer: %w", err)
	}

	// Unblank the framebuffer
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fbFile.Fd(), 0x4611, 0)
	if errno != 0 {
		log.Printf("Warning: Failed to unblank framebuffer: %v", errno)
	}

	// Turn on the backlight
	if err := os.WriteFile("/sys/class/backlight/backlight_gpio/brightness", []byte("1"), 0644); err != nil {
		log.Printf("Warning: Failed to turn on backlight: %v", err)
	}

	r := &Renderer{
		bus:    eventBus,
		lib:    lib,
		fbFile: fbFile,
		mmap:   mmap,
		canvas: image.NewRGBA(image.Rect(0, 0, 320, 240)),
	}

	go r.listen()
	r.render() // initial render

	return r, nil
}

func (r *Renderer) Close() {
	if r.mmap != nil {
		unix.Munmap(r.mmap)
	}
	if r.fbFile != nil {
		r.fbFile.Close()
	}
}

func (r *Renderer) listen() {
	ch := r.bus.Subscribe()
	for ev := range ch {
		needsRender := false
		switch ev.Type {
		case bus.EventPlayerStateChanged, bus.EventPlayerProgressTick:
			if state, ok := ev.Payload.(player.PlaybackState); ok {
				r.playState = state
				needsRender = true
			}
		case bus.EventMenuUpdate:
			if state, ok := ev.Payload.(bus.MenuState); ok {
				r.menuState = state
				needsRender = true
			}
		}

		if needsRender {
			r.render()
		}
	}
}

func (r *Renderer) render() {
	// 1. Clear background (dark blue)
	draw.Draw(r.canvas, r.canvas.Bounds(), &image.Uniform{color.RGBA{0, 0, 50, 255}}, image.Point{}, draw.Src)

	r.renderPlayer()
	if r.menuState.Active {
		r.renderMenu()
	}

	// Copy to hardware
	copyToRGB565(r.mmap, r.canvas)
}

func (r *Renderer) renderMenu() {
	// Draw opaque background over part of the screen
	draw.Draw(r.canvas, image.Rect(0, 30, 320, 240), &image.Uniform{color.RGBA{0, 0, 0, 230}}, image.Point{}, draw.Src)
	addLabel(r.canvas, 10, 50, "Library Menu", color.RGBA{200, 255, 200, 255})

	books, ok := r.menuState.Books.([]library.Audiobook)
	if !ok {
		books = []library.Audiobook{}
	}

	// Figure out scroll offset to keep selected item on screen
	// Menu Index 0 is Settings. Books are 1..N
	scrollStart := 0
	if r.menuState.Index > 5 {
		scrollStart = r.menuState.Index - 5
	}

	y := 70

	// Render Settings Item
	if scrollStart == 0 {
		c := color.RGBA{200, 200, 200, 255}
		prefix := "  "
		if r.menuState.Index == 0 {
			c = color.RGBA{255, 255, 50, 255}
			prefix = "> "
		}
		_, _, timeout, _ := r.lib.GetSystemState()
		timeoutStr := "Off"
		if timeout > 0 {
			timeoutStr = fmt.Sprintf("%dm", timeout)
		}
		addLabel(r.canvas, 10, y, fmt.Sprintf("%sSettings: Screen Timeout [%s]", prefix, timeoutStr), c)
		y += 25
	}

	// Render Books
	for i := scrollStart; i < len(books); i++ {
		if y > 220 {
			break // off screen
		}
		b := books[i]
		title := b.Title
		if title == "" {
			title = filepath.Base(b.FilePath)
		}
		if len(title) > 42 {
			title = title[:39] + "..."
		}

		c := color.RGBA{200, 200, 200, 255}
		prefix := "  "
		if i+1 == r.menuState.Index {
			c = color.RGBA{255, 255, 255, 255}
			prefix = "> "
		} else if filepath.Base(b.FilePath) == filepath.Base(r.playState.FilePath) {
			c = color.RGBA{150, 200, 255, 255}
			prefix = "* "
		}
		addLabel(r.canvas, 10, y, prefix+title, c)
		y += 25
	}
}

func (r *Renderer) renderPlayer() {
	// 2. Query Library for metadata
	var book *library.Audiobook
	var chapterTitle string
	var chapterStart float64
	chapterEnd := r.playState.Duration

	if r.playState.FilePath != "" {
		b, err := r.lib.GetByFilename(r.playState.FilePath)
		if err == nil {
			book = b

			// Find current chapter
			for i, chap := range book.Chapters {
				if r.playState.Position >= chap.StartTime-0.5 {
					chapterTitle = chap.Title
					chapterStart = chap.StartTime
					if i+1 < len(book.Chapters) {
						chapterEnd = book.Chapters[i+1].StartTime
					}
				} else {
					break
				}
			}
		}
	}

	// 3. Draw Book Title
	title := r.playState.FilePath
	if title == "" {
		title = "Bedside Audio"
	} else if book != nil && book.Title != "" {
		title = book.Title
	}

	// Truncate long titles loosely
	if len(title) > 44 {
		title = title[:41] + "..."
	}
	addLabel(r.canvas, 10, 30, title, color.RGBA{255, 255, 255, 255})

	// 4. Draw Chapter Title
	if chapterTitle != "" {
		if len(chapterTitle) > 40 {
			chapterTitle = chapterTitle[:37] + "..."
		}
		addLabel(r.canvas, 10, 70, chapterTitle, color.RGBA{200, 200, 255, 255})
	}

	// 5. Draw State
	status := "Paused"
	if title == "Bedside Audio" {
		status = "Idle"
	} else if !r.playState.Paused {
		status = "Playing"
	}

	if r.scrubMode {
		status = fmt.Sprintf("%s  |  Mode: Scrub", status)
	} else {
		status = fmt.Sprintf("%s  |  Vol: %d%%", status, int(r.playState.Volume))
	}
	addLabel(r.canvas, 10, 110, status, color.RGBA{150, 255, 150, 255})

	// 6. Draw Chapter Progress Bar
	if title != "Bedside Audio" {
		chapDur := chapterEnd - chapterStart
		chapPos := r.playState.Position - chapterStart
		if chapDur > 0 {
			pct := chapPos / chapDur
			if pct > 1 {
				pct = 1
			} else if pct < 0 {
				pct = 0
			}
			barWidth := 300
			filled := int(float64(barWidth) * pct)

			// Bar outline
			draw.Draw(r.canvas, image.Rect(10, 150, 10+barWidth, 160), &image.Uniform{color.RGBA{100, 100, 100, 255}}, image.Point{}, draw.Src)
			// Bar fill
			draw.Draw(r.canvas, image.Rect(10, 150, 10+filled, 160), &image.Uniform{color.RGBA{100, 255, 100, 255}}, image.Point{}, draw.Src)
		}
	}

	// 7. Draw Time Strings
	if title != "Bedside Audio" {
		// Chapter time
		chapDur := chapterEnd - chapterStart
		chapPos := r.playState.Position - chapterStart

		chapTimeStr := fmt.Sprintf("Chap: %02d:%02d / %02d:%02d",
			int(chapPos)/60, int(chapPos)%60,
			int(chapDur)/60, int(chapDur)%60,
		)
		addLabel(r.canvas, 10, 180, chapTimeStr, color.RGBA{200, 200, 200, 255})

		// Total time
		totalTimeStr := fmt.Sprintf("Total: %02dh%02dm / %02dh%02dm",
			int(r.playState.Position)/3600, (int(r.playState.Position)%3600)/60,
			int(r.playState.Duration)/3600, (int(r.playState.Duration)%3600)/60,
		)
		addLabel(r.canvas, 10, 200, totalTimeStr, color.RGBA{150, 150, 150, 255})
	}
}

func addLabel(img *image.RGBA, x, y int, label string, col color.RGBA) {
	point := fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)}
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: basicfont.Face7x13,
		Dot:  point,
	}
	d.DrawString(label)
}

// copyToRGB565 converts the RGBA canvas to RGB565 and writes it to the mmap.
func copyToRGB565(dst []byte, src *image.RGBA) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	offset := 0
	for y := 0; y < h; y++ {
		srcOffset := src.PixOffset(0, y)
		for x := 0; x < w; x++ {
			r := src.Pix[srcOffset]
			g := src.Pix[srcOffset+1]
			b := src.Pix[srcOffset+2]
			srcOffset += 4

			// RGB565 encoding
			r5 := uint16(r) >> 3
			g6 := uint16(g) >> 2
			b5 := uint16(b) >> 3
			rgb565 := (r5 << 11) | (g6 << 5) | b5

			// Little-endian order for the framebuffer
			dst[offset] = byte(rgb565)
			dst[offset+1] = byte(rgb565 >> 8)
			offset += 2
		}
	}
}

// SetScrubMode updates the scrub mode toggle for the UI
func (r *Renderer) SetScrubMode(scrub bool) {
	r.scrubMode = scrub
	r.render()
}
