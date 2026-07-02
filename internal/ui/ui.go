package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/unix"
)

// UI palette. color.RGBA values cannot be constants, so these are package vars.
var (
	colorBackground   = color.RGBA{0, 0, 50, 255}      // screen background
	colorMenuOverlay  = color.RGBA{0, 0, 0, 230}       // dimmed menu backdrop
	colorText         = color.RGBA{255, 255, 255, 255} // primary text / selected book
	colorMuted        = color.RGBA{200, 200, 200, 255} // secondary text / menu row
	colorFaint        = color.RGBA{150, 150, 150, 255} // total-time line
	colorChapter      = color.RGBA{200, 200, 255, 255} // chapter title
	colorStatus       = color.RGBA{150, 255, 150, 255} // play/pause status line
	colorBarTrack     = color.RGBA{100, 100, 100, 255} // progress bar background
	colorBarFill      = color.RGBA{100, 255, 100, 255} // progress bar fill
	colorMenuHeader   = color.RGBA{200, 255, 200, 255} // "Library Menu" heading
	colorMenuSelected = color.RGBA{255, 255, 50, 255}  // highlighted settings row
	colorNowPlaying   = color.RGBA{150, 200, 255, 255} // book currently loaded
)

type Renderer struct {
	bus    *bus.Bus
	lib    *library.Manager
	fbFile *os.File
	mmap   []byte
	canvas *image.RGBA
	mu     sync.Mutex

	// Local state
	playState   player.PlaybackState
	menuState   bus.MenuState
	encoderMode string
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
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Clear background (dark blue)
	draw.Draw(r.canvas, r.canvas.Bounds(), &image.Uniform{colorBackground}, image.Point{}, draw.Src)

	r.renderPlayer()
	if r.menuState.Active {
		r.renderMenu()
	}

	// Copy to hardware
	copyToRGB565(r.mmap, r.canvas)
}

// Menu layout geometry, in pixels.
const (
	menuFirstRowY = 70
	menuRowHeight = 25
	menuBottomY   = 220
)

func (r *Renderer) renderMenu() {
	r.drawMenuBackground()

	books := r.menuBooks()
	scrollStart := menuScrollStart(r.menuState.Index)
	y := menuFirstRowY

	// Menu index 0 is the Settings row; books follow at indices 1..N. The
	// Settings row is only visible before the list has scrolled.
	if scrollStart == 0 {
		r.drawSettingsRow(y)
		y += menuRowHeight
	}

	for i := scrollStart; i < len(books); i++ {
		if y > menuBottomY {
			break // off screen
		}
		r.drawBookRow(y, i, books[i])
		y += menuRowHeight
	}
}

func (r *Renderer) drawMenuBackground() {
	fillRect(r.canvas, 0, 30, 320, 210, colorMenuOverlay)
	addLabel(r.canvas, 10, 50, "Library Menu", colorMenuHeader)
}

// menuBooks returns the audiobook list carried on the menu state, or nil when
// the payload is missing or of an unexpected type.
func (r *Renderer) menuBooks() []library.Audiobook {
	books, ok := r.menuState.Books.([]library.Audiobook)
	if !ok {
		return nil
	}
	return books
}

// menuScrollStart returns the first list row to draw so the selected item stays
// on screen once the selection moves past the visible window.
func menuScrollStart(index int) int {
	const visibleBeforeSelection = 5
	if index > visibleBeforeSelection {
		return index - visibleBeforeSelection
	}
	return 0
}

func (r *Renderer) drawSettingsRow(y int) {
	c := colorMuted
	prefix := "  "
	if r.menuState.Index == 0 {
		c = colorMenuSelected
		prefix = "> "
	}
	sysState, _ := r.lib.GetSystemState()
	label := fmt.Sprintf("%sSettings: Screen Timeout [%s]", prefix, timeoutLabel(sysState.Timeout))
	addLabel(r.canvas, 10, y, label, c)
}

// timeoutLabel renders the screen-timeout setting; non-positive means off.
func timeoutLabel(minutes int) string {
	if minutes <= 0 {
		return "Off"
	}
	return fmt.Sprintf("%dm", minutes)
}

func (r *Renderer) drawBookRow(y, i int, b library.Audiobook) {
	prefix, c := r.bookRowStyle(i, b)
	addLabel(r.canvas, 10, y, prefix+bookTitle(b), c)
}

// bookRowStyle returns the marker prefix and color for book row i: the selected
// row wins, then the currently-playing book, then a plain row.
func (r *Renderer) bookRowStyle(i int, b library.Audiobook) (string, color.RGBA) {
	switch {
	case i+1 == r.menuState.Index:
		return "> ", colorText
	case filepath.Base(b.FilePath) == filepath.Base(r.playState.FilePath):
		return "* ", colorNowPlaying
	default:
		return "  ", colorMuted
	}
}

// bookTitle returns the display title for a book, falling back to the file's
// base name, truncated to fit the menu width.
func bookTitle(b library.Audiobook) string {
	title := b.Title
	if title == "" {
		title = filepath.Base(b.FilePath)
	}
	return truncate(title, 42)
}

// idleTitle is shown on the player screen when no audiobook is loaded.
const idleTitle = "Bedside Audio"

// chapterInfo describes the currently-playing chapter's title and bounds,
// resolved from library metadata and the current playback position.
type chapterInfo struct {
	title string
	start float64
	end   float64
}

func (r *Renderer) renderPlayer() {
	book := r.currentBook()
	chapter := r.resolveChapter(book)
	title := r.displayTitle(book)
	idle := title == idleTitle

	r.drawTitle(title)
	r.drawChapterTitle(chapter.title)
	r.drawStatus(idle)

	if idle {
		return
	}
	r.drawChapterProgress(chapter)
	r.drawTimes(chapter)
}

// currentBook returns the library metadata for the loaded file, or nil when
// nothing is playing or the file is not in the library.
func (r *Renderer) currentBook() *library.Audiobook {
	if r.playState.FilePath == "" {
		return nil
	}
	book, err := r.lib.GetByFilename(r.playState.FilePath)
	if err != nil {
		return nil
	}
	return book
}

// resolveChapter finds the chapter containing the current playback position.
// The end time falls back to the book (or stream) duration when the position
// precedes the first chapter or when metadata is incomplete.
func (r *Renderer) resolveChapter(book *library.Audiobook) chapterInfo {
	info := chapterInfo{end: r.playState.Duration}
	if book == nil {
		return info
	}

	// Default end to book duration in case mpv hasn't reported it yet.
	info.end = book.Duration
	if info.end == 0 {
		info.end = r.playState.Duration // ultimate fallback
	}

	for i, chap := range book.Chapters {
		if r.playState.Position < chap.StartTime-0.5 {
			break
		}
		info.title = chap.Title
		info.start = chap.StartTime
		if i+1 < len(book.Chapters) {
			info.end = book.Chapters[i+1].StartTime
		} else if book.Duration > 0 {
			info.end = book.Duration
		}
	}
	return info
}

// displayTitle picks the label for the loaded file: the library title when
// known, otherwise the raw file path, or idleTitle when nothing is playing.
func (r *Renderer) displayTitle(book *library.Audiobook) string {
	if r.playState.FilePath == "" {
		return idleTitle
	}
	if book != nil && book.Title != "" {
		return book.Title
	}
	return r.playState.FilePath
}

func (r *Renderer) drawTitle(title string) {
	addLabel(r.canvas, 10, 30, truncate(title, 44), colorText)
}

func (r *Renderer) drawChapterTitle(title string) {
	if title == "" {
		return
	}
	addLabel(r.canvas, 10, 70, truncate(title, 40), colorChapter)
}

func (r *Renderer) drawStatus(idle bool) {
	status := "Paused"
	switch {
	case idle:
		status = "Idle"
	case !r.playState.Paused:
		status = "Playing"
	}

	if r.encoderMode == "scrub" {
		status += "  |  Mode: Scrub"
	} else {
		status += fmt.Sprintf("  |  Vol: %d%%", int(r.playState.Volume))
	}
	addLabel(r.canvas, 10, 110, status, colorStatus)
}

func (r *Renderer) drawChapterProgress(chapter chapterInfo) {
	dur := chapter.end - chapter.start
	if dur <= 0 {
		return
	}
	pct := clamp01((r.playState.Position - chapter.start) / dur)

	const barWidth = 300
	fillRect(r.canvas, 10, 150, barWidth, 10, colorBarTrack)
	fillRect(r.canvas, 10, 150, int(float64(barWidth)*pct), 10, colorBarFill)
}

func (r *Renderer) drawTimes(chapter chapterInfo) {
	chapPos := r.playState.Position - chapter.start
	chapDur := chapter.end - chapter.start
	addLabel(r.canvas, 10, 180,
		fmt.Sprintf("Chap: %s / %s", formatMinSec(chapPos), formatMinSec(chapDur)),
		colorMuted)

	addLabel(r.canvas, 10, 200,
		fmt.Sprintf("Total: %s / %s", formatHourMin(r.playState.Position), formatHourMin(r.playState.Duration)),
		colorFaint)
}

// fillRect paints a solid w×h rectangle with its top-left corner at (x, y).
func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Src)
}

// truncate shortens s to at most max bytes, replacing the tail with "..." when
// it would otherwise overflow.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// clamp01 constrains v to the [0, 1] range.
func clamp01(v float64) float64 {
	switch {
	case v > 1:
		return 1
	case v < 0:
		return 0
	default:
		return v
	}
}

// formatMinSec renders a duration in seconds as MM:SS.
func formatMinSec(seconds float64) string {
	s := int(seconds)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// formatHourMin renders a duration in seconds as HHhMMm.
func formatHourMin(seconds float64) string {
	s := int(seconds)
	return fmt.Sprintf("%02dh%02dm", s/3600, (s%3600)/60)
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

// SetEncoderMode updates the encoder mode display
func (r *Renderer) SetEncoderMode(mode string) {
	r.mu.Lock()
	r.encoderMode = mode
	r.mu.Unlock()
	r.render()
}
