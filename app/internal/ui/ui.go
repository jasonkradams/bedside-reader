package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonkradams/bedside-reader/internal/bus"
	"github.com/jasonkradams/bedside-reader/internal/display"
	"github.com/jasonkradams/bedside-reader/internal/library"
	"github.com/jasonkradams/bedside-reader/internal/player"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/sys/unix"
)

// Warm "nightstand" palette: low blue light for a dark bedroom, cream/amber on a
// near-black warm ground, echoing the gold-on-dark of typical audiobook covers.
// color.RGBA values can't be consts, so these are package vars.
var (
	colorBg         = color.RGBA{18, 15, 12, 255}    // near-black warm ground
	colorPanel      = color.RGBA{30, 25, 19, 255}    // lifted panel / placeholder
	colorText       = color.RGBA{242, 234, 218, 255} // warm cream, primary
	colorMuted      = color.RGBA{178, 162, 138, 255} // warm taupe, secondary
	colorFaint      = color.RGBA{120, 108, 92, 255}  // faint tertiary
	colorAccent     = color.RGBA{226, 170, 98, 255}  // amber/gold accent
	colorTrack      = color.RGBA{70, 58, 44, 255}    // progress track
	colorStatus     = color.RGBA{158, 196, 138, 255} // soft sage, playing/now-playing
	colorCoverFrame = color.RGBA{58, 48, 36, 255}    // cover border
	colorMenuScrim  = color.RGBA{12, 9, 7, 236}      // menu backdrop
	colorMenuHeader = color.RGBA{226, 170, 98, 255}  // menu heading (amber)
	colorMenuSel    = color.RGBA{255, 224, 170, 255} // selected row (bright amber)
	colorMenuSelBar = color.RGBA{226, 170, 98, 255}  // selection accent bar
	colorWifiOn     = color.RGBA{158, 196, 138, 255}
	colorWifiOff    = color.RGBA{150, 90, 74, 255}
)

// Layout geometry for the 320x240 panel (cover-hero split).
const (
	pad        = 12
	coverX     = pad
	coverY     = 18
	coverSize  = 150
	textX      = coverX + coverSize + 14 // right text column
	textW      = panelWidth - textX - pad
	progressY  = 182
	barH       = 6
	chapTimesY = 203
	bookTimesY = 218
	statusY    = 233
	wifiSlot   = 16 // space reserved for the bottom-right Wi-Fi icon
)

type Renderer struct {
	bus       *bus.Bus
	lib       *library.Manager
	fbFile    *os.File
	frame     []byte // RGB565 frame buffer, written to the panel each render
	canvas    *image.RGBA
	renderReq chan struct{} // coalesced render requests
	mu        sync.Mutex

	fonts      *fontManager
	fontChoice FontChoice

	// Cached scaled cover for the currently-loaded book.
	coverID  string
	coverImg *image.RGBA

	// Local state
	playState     player.PlaybackState
	menuState     bus.MenuState
	encoderMode   string
	wifiConnected bool
	lastTickSec   int // whole-second of the last progress-driven render
}

// Panel geometry: 320x240 ST7789 in landscape, 16bpp (RGB565).
const (
	panelWidth    = 320
	panelHeight   = 240
	panelGeometry = "320,240" // matches the sysfs virtual_size of the panel fb
)

// fbInfo is the identifying sysfs metadata for one /sys/class/graphics/fbN.
type fbInfo struct {
	dev      string // e.g. "/dev/fb2"
	name     string // driver name, e.g. "panel-mipi-dbid"
	geometry string // virtual_size, e.g. "320,240"
}

// pickPanelFB chooses the framebuffer backing the SPI panel: prefer one whose
// driver name identifies the panel-mipi-dbi device, otherwise fall back to the
// one matching the panel's 320x240 geometry. Returns false if neither matches.
func pickPanelFB(fbs []fbInfo) (string, bool) {
	for _, fb := range fbs {
		if strings.Contains(fb.name, "panel") || strings.Contains(fb.name, "mipi-dbi") {
			return fb.dev, true
		}
	}
	for _, fb := range fbs {
		if fb.geometry == panelGeometry {
			return fb.dev, true
		}
	}
	return "", false
}

// panelFramebuffer locates the panel's /dev/fbN. The index is NOT fixed: the
// firmware registers its own framebuffers first (fb0/fb1 = HDMI/simpledrm), so
// the ST7789 lands on fb2 here — but that ordering isn't guaranteed. Discover it
// by sysfs metadata so a firmware/overlay change can't silently send the UI to
// the HDMI framebuffer instead of the panel.
func panelFramebuffer() (string, error) {
	names, _ := filepath.Glob("/sys/class/graphics/fb*/name")
	fbs := make([]fbInfo, 0, len(names))
	for _, namePath := range names {
		dir := filepath.Dir(namePath)       // /sys/class/graphics/fb2
		dev := "/dev/" + filepath.Base(dir) // /dev/fb2
		fbs = append(fbs, fbInfo{
			dev:      dev,
			name:     readTrim(namePath),
			geometry: readTrim(filepath.Join(dir, "virtual_size")),
		})
	}
	if dev, ok := pickPanelFB(fbs); ok {
		return dev, nil
	}
	return "", fmt.Errorf("no panel framebuffer among %d devices", len(fbs))
}

// readTrim reads a sysfs attribute and trims trailing whitespace, "" on error.
func readTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// panelOpenTimeout bounds how long New waits for the panel framebuffer to be
// openable. The ST7789 probes late over SPI (~20s into boot) and udev applies
// its `video` group a moment after the node appears, so opening too early
// returns ENOENT or EACCES. Retrying avoids crashing on boot (which otherwise
// forces a systemd restart and adds ~4-5s to first playback).
const panelOpenTimeout = 20 * time.Second

// openPanelFramebuffer discovers and opens the panel's framebuffer, retrying
// until it succeeds or timeout elapses (see panelOpenTimeout for why).
func openPanelFramebuffer(timeout time.Duration) (*os.File, string, error) {
	deadline := time.Now().Add(timeout)
	for attempt := 0; ; attempt++ {
		path, err := panelFramebuffer()
		if err == nil {
			f, oerr := os.OpenFile(path, os.O_RDWR, 0)
			if oerr == nil {
				return f, path, nil
			}
			err = oerr
		}
		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("panel framebuffer not ready after %s: %w", timeout, err)
		}
		if attempt == 0 {
			log.Printf("UI: waiting for panel framebuffer to be ready: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func New(eventBus *bus.Bus, lib *library.Manager) (*Renderer, error) {
	fbFile, fbPath, err := openPanelFramebuffer(panelOpenTimeout)
	if err != nil {
		return nil, err
	}
	log.Printf("UI rendering to panel framebuffer %s", fbPath)

	// Unblank the framebuffer
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fbFile.Fd(), 0x4611, 0)
	if errno != 0 {
		log.Printf("Warning: Failed to unblank framebuffer: %v", errno)
	}

	// Initial backlight
	display.SetBacklight(true)

	r := &Renderer{
		bus:        eventBus,
		lib:        lib,
		fbFile:     fbFile,
		frame:      make([]byte, panelWidth*panelHeight*2),
		canvas:     image.NewRGBA(image.Rect(0, 0, panelWidth, panelHeight)),
		renderReq:  make(chan struct{}, 1),
		fonts:      newFontManager(),
		fontChoice: fontByID(defaultFontID),
	}

	go r.renderLoop()
	go r.listen()
	go r.pollWiFi()
	go r.periodicRefresh()
	r.requestRender() // initial render

	return r, nil
}

func (r *Renderer) Close() {
	if r.fbFile != nil {
		r.fbFile.Close()
	}
}

// SetFont switches the active typeface (by registry ID) and repaints.
func (r *Renderer) SetFont(id string) {
	r.mu.Lock()
	r.fontChoice = fontByID(id)
	r.mu.Unlock()
	r.requestRender()
}

func (r *Renderer) pollWiFi() {
	for {
		connected := false
		data, err := os.ReadFile("/sys/class/net/wlan0/carrier")
		if err == nil && len(data) > 0 && data[0] == '1' {
			connected = true
		}

		r.mu.Lock()
		changed := r.wifiConnected != connected
		r.wifiConnected = connected
		r.mu.Unlock()

		if changed {
			r.requestRender()
		}

		time.Sleep(2 * time.Second)
	}
}

func (r *Renderer) listen() {
	ch := r.bus.Subscribe()
	for ev := range ch {
		needsRender := false
		switch ev.Type {
		case bus.EventPlayerStateChanged:
			if state, ok := ev.Payload.(player.PlaybackState); ok {
				r.playState = state
				needsRender = true
			}
		case bus.EventPlayerProgressTick:
			// Ticks arrive many times a second but the on-screen times change
			// once a second; only repaint on the whole-second boundary.
			if state, ok := ev.Payload.(player.PlaybackState); ok {
				r.playState = state
				if sec := int(state.Position); sec != r.lastTickSec {
					r.lastTickSec = sec
					needsRender = true
				}
			}
		case bus.EventMenuUpdate:
			if state, ok := ev.Payload.(bus.MenuState); ok {
				r.menuState = state
				needsRender = true
			}
		case bus.EventLibraryScanComplete:
			// A scan may have just extracted the current book's cover art;
			// repaint so it appears without waiting for the next event.
			needsRender = true
		}

		if needsRender {
			r.requestRender()
		}
	}
}

// requestRender coalesces render requests: bursts collapse into one frame.
func (r *Renderer) requestRender() {
	select {
	case r.renderReq <- struct{}{}:
	default:
	}
}

// renderLoop caps the refresh rate; requests during the sleep coalesce.
func (r *Renderer) renderLoop() {
	const minInterval = 66 * time.Millisecond // ~15 fps
	for range r.renderReq {
		r.render()
		time.Sleep(minInterval)
	}
}

// periodicRefresh redraws slowly when idle so the panel self-heals and reflects
// current state after waking.
func (r *Renderer) periodicRefresh() {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		r.requestRender()
	}
}

func (r *Renderer) render() {
	r.mu.Lock()
	defer r.mu.Unlock()

	draw.Draw(r.canvas, r.canvas.Bounds(), &image.Uniform{colorBg}, image.Point{}, draw.Src)

	r.renderPlayer()
	if r.menuState.Active {
		r.renderMenu()
	}
	r.drawWiFiIcon(panelWidth-pad-10, statusY-9, r.wifiConnected)

	copyToRGB565(r.frame, r.canvas)
	r.flush()
}

// flush writes the frame through the fbdev write path, not mmap: mmap dirty-page
// tracking stops flushing the static top of the screen and freezes it.
func (r *Renderer) flush() {
	for off := 0; off < len(r.frame); {
		n, err := r.fbFile.WriteAt(r.frame[off:], int64(off))
		if err != nil {
			log.Printf("framebuffer write failed at offset %d: %v", off, err)
			return
		}
		if n <= 0 {
			return
		}
		off += n
	}
}

// idleTitle is shown on the player screen when no audiobook is loaded.
const idleTitle = "Bedside Audio"

// chapterInfo describes the currently-playing chapter's title and bounds.
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

	r.ensureCover(book)
	r.drawCover(title)
	r.drawInfoColumn(book, title, chapter)

	if idle {
		return
	}
	r.drawProgress(chapter)
	r.drawTimes(chapter, r.bookDuration(book))
	r.drawStatus()
}

// bookDuration prefers mpv's live duration, falling back to library metadata.
func (r *Renderer) bookDuration(book *library.Audiobook) float64 {
	if r.playState.Duration > 0 {
		return r.playState.Duration
	}
	if book != nil {
		return book.Duration
	}
	return 0
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

// ensureCover decodes and scales the current book's cover once, caching the
// result. It is a no-op when the same book is already cached.
func (r *Renderer) ensureCover(book *library.Audiobook) {
	if book == nil || book.CoverHash == "" {
		r.coverID = ""
		r.coverImg = nil
		return
	}
	if r.coverID == book.ID && r.coverImg != nil {
		return
	}
	r.coverID = book.ID
	r.coverImg = nil

	f, err := os.Open(r.lib.CoverPath(book.ID))
	if err != nil {
		return
	}
	defer f.Close()
	src, err := jpeg.Decode(f)
	if err != nil {
		return
	}
	dst := image.NewRGBA(image.Rect(0, 0, coverSize, coverSize))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	r.coverImg = dst
}

// drawCover paints the cached cover (or a styled placeholder) in a subtle frame.
func (r *Renderer) drawCover(title string) {
	fillRect(r.canvas, coverX-1, coverY-1, coverSize+2, coverSize+2, colorCoverFrame)
	if r.coverImg != nil {
		draw.Draw(r.canvas,
			image.Rect(coverX, coverY, coverX+coverSize, coverY+coverSize),
			r.coverImg, image.Point{}, draw.Src)
		return
	}
	// Placeholder: the title's first letter, large and faint.
	fillRect(r.canvas, coverX, coverY, coverSize, coverSize, colorPanel)
	glyph := "♪"
	if t := strings.TrimSpace(title); t != "" && t != idleTitle {
		glyph = strings.ToUpper(string([]rune(t)[:1]))
	}
	face := r.fonts.face(r.fontChoice.bold, 72)
	w := textWidth(face, glyph)
	drawText(r.canvas, coverX+(coverSize-w)/2, coverY+coverSize/2+26, glyph, face, colorFaint)
}

// drawInfoColumn draws title / author / chapter. The title wraps to 4 lines;
// author and chapter are dropped if they'd overrun the progress bar.
func (r *Renderer) drawInfoColumn(book *library.Audiobook, title string, chapter chapterInfo) {
	const maxY = progressY - 6

	titleFace := r.fonts.face(r.fontChoice.bold, sizeTitle)
	y := coverY + lineHeight(titleFace) - 2
	for _, line := range wrapLines(titleFace, title, textW, 4) {
		drawText(r.canvas, textX, y, line, titleFace, colorText)
		y += lineHeight(titleFace)
	}

	if book != nil && book.Author != "" {
		af := r.fonts.face(r.fontChoice.regular, sizeBody)
		if y+lineHeight(af) <= maxY {
			y += 4
			drawText(r.canvas, textX, y+lineHeight(af)-4, ellipsize(af, book.Author, textW), af, colorMuted)
			y += lineHeight(af)
		}
	}

	if chapter.title != "" {
		cf := r.fonts.face(r.fontChoice.regular, sizeChapter)
		y += 8
		for _, line := range wrapLines(cf, chapter.title, textW, 2) {
			if y+lineHeight(cf) > maxY {
				break
			}
			drawText(r.canvas, textX, y+lineHeight(cf)-4, line, cf, colorAccent)
			y += lineHeight(cf)
		}
	}
}

// resolveChapter finds the chapter containing the current playback position.
// The end time falls back to the book (or stream) duration when the position
// precedes the first chapter or when metadata is incomplete.
func (r *Renderer) resolveChapter(book *library.Audiobook) chapterInfo {
	info := chapterInfo{end: r.playState.Duration}
	if book == nil {
		return info
	}

	info.end = book.Duration
	if info.end == 0 {
		info.end = r.playState.Duration
	}

	idx := library.ChapterIndexAt(book.Chapters, r.playState.Position)
	if idx < 0 {
		return info
	}
	info.title = book.Chapters[idx].Title
	info.start = book.Chapters[idx].StartTime
	if idx+1 < len(book.Chapters) {
		info.end = book.Chapters[idx+1].StartTime
	} else if book.Duration > 0 {
		info.end = book.Duration
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

func (r *Renderer) drawProgress(chapter chapterInfo) {
	barW := panelWidth - 2*pad
	dur := chapter.end - chapter.start
	pct := 0.0
	if dur > 0 {
		pct = clamp01((r.playState.Position - chapter.start) / dur)
	}
	fillRect(r.canvas, pad, progressY, barW, barH, colorTrack)
	fillRect(r.canvas, pad, progressY, int(float64(barW)*pct), barH, colorAccent)
	// playhead knob
	kx := pad + int(float64(barW)*pct)
	fillRect(r.canvas, kx-1, progressY-3, 3, barH+6, colorText)
}

// drawTimes shows chapter and whole-book rows as "elapsed / total" with the
// remaining time on the right.
func (r *Renderer) drawTimes(chapter chapterInfo, bookDur float64) {
	face := r.fonts.face(r.fontChoice.regular, sizeSmall)

	chapPos := nonNeg(r.playState.Position - chapter.start)
	chapDur := chapter.end - chapter.start
	drawText(r.canvas, pad, chapTimesY,
		fmt.Sprintf("Chapter %s / %s", formatMinSec(chapPos), formatMinSec(chapDur)), face, colorMuted)
	drawRightText(r.canvas, panelWidth-pad, chapTimesY,
		"-"+formatMinSec(nonNeg(chapDur-chapPos)), face, colorFaint)

	bookPos := nonNeg(r.playState.Position)
	drawText(r.canvas, pad, bookTimesY,
		fmt.Sprintf("Book %s / %s", formatHourMin(bookPos), formatHourMin(bookDur)), face, colorMuted)
	drawRightText(r.canvas, panelWidth-pad, bookTimesY,
		"-"+formatHourMin(nonNeg(bookDur-bookPos)), face, colorFaint)
}

// nonNeg clamps negatives to zero.
func nonNeg(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

func (r *Renderer) drawStatus() {
	face := r.fonts.face(r.fontChoice.regular, sizeSmall)
	status := "❚❚ Paused"
	col := colorMuted
	if !r.playState.Paused {
		status = "▶ Playing"
		col = colorStatus
	}
	drawText(r.canvas, pad, statusY, status, face, col)

	var right string
	if r.encoderMode == "scrub" {
		right = "Scrub"
	} else {
		right = fmt.Sprintf("Vol %d%%", int(r.playState.Volume))
	}
	drawRightText(r.canvas, panelWidth-pad-wifiSlot, statusY, right, face, colorMuted)
}

// ---- Library / settings menu ----

// SettingsRowCount is the number of settings rows shown above the book list in
// the menu. App and the renderer must agree on this so selection indices align
// (rows 0..SettingsRowCount-1 are settings; SettingsRowCount.. are books).
const SettingsRowCount = 2

// menuSetting is one non-book row at the top of the menu.
type menuSetting struct {
	label string
	value string
}

// settingsRows returns the fixed settings rows shown above the book list, in
// order. The count here must match App's settingsRowCount so selection indices
// line up (rows 0..N-1 are settings, N.. are books).
func (r *Renderer) settingsRows() []menuSetting {
	sysState, _ := r.lib.GetSystemState()
	return []menuSetting{
		{label: "Screen Timeout", value: timeoutLabel(sysState.Timeout)},
		{label: "Font", value: r.fontChoice.Name},
	}
}

// Menu layout geometry, in pixels.
const (
	menuHeaderY   = 30
	menuFirstRowY = 58
	menuRowHeight = 26
	menuBottomY   = 232
)

func (r *Renderer) renderMenu() {
	// Full-screen warm scrim.
	fillRect(r.canvas, 0, 0, panelWidth, panelHeight, colorMenuScrim)
	hf := r.fonts.face(r.fontChoice.bold, sizeMenuHdr)
	drawText(r.canvas, pad, menuHeaderY, "Library", hf, colorMenuHeader)

	rowFace := r.fonts.face(r.fontChoice.regular, sizeMenuRow)
	settings := r.settingsRows()
	books := r.menuBooks()
	scrollStart := menuScrollStart(r.menuState.Index)
	y := menuFirstRowY

	// Settings rows appear only before the list has scrolled.
	if scrollStart == 0 {
		for i, s := range settings {
			r.drawMenuRow(y, i, fmt.Sprintf("%s: %s", s.label, s.value), rowFace, colorMuted)
			y += menuRowHeight
		}
	}

	n := SettingsRowCount
	for i := scrollStart; i < len(books); i++ {
		if y > menuBottomY {
			break
		}
		prefix, col := r.bookRowStyle(i, books[i])
		label := prefix + bookTitle(books[i])
		r.drawMenuRow(y, i+n, ellipsize(rowFace, label, panelWidth-2*pad-6), rowFace, col)
		y += menuRowHeight
	}
}

// drawMenuRow draws one menu row, highlighting it when it is the selection.
func (r *Renderer) drawMenuRow(y, index int, text string, face font.Face, col color.RGBA) {
	if index == r.menuState.Index {
		fillRect(r.canvas, 0, y-menuRowHeight+8, panelWidth, menuRowHeight, colorPanel)
		fillRect(r.canvas, 0, y-menuRowHeight+8, 3, menuRowHeight, colorMenuSelBar)
		col = colorMenuSel
	}
	drawText(r.canvas, pad, y, text, face, col)
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

// timeoutLabel renders the screen-timeout setting; non-positive means off.
func timeoutLabel(minutes int) string {
	if minutes <= 0 {
		return "Off"
	}
	return fmt.Sprintf("%dm", minutes)
}

// bookRowStyle returns the marker prefix and color for book row i: the selected
// row wins, then the currently-playing book, then a plain row. Selection is
// offset by the settings rows drawn above the list.
func (r *Renderer) bookRowStyle(i int, b library.Audiobook) (string, color.RGBA) {
	switch {
	case i+SettingsRowCount == r.menuState.Index:
		return "> ", colorText
	case filepath.Base(b.FilePath) == filepath.Base(r.playState.FilePath):
		return "* ", colorStatus
	default:
		return "  ", colorMuted
	}
}

// bookTitle returns the display title for a book, falling back to the file's
// base name.
func bookTitle(b library.Audiobook) string {
	if b.Title != "" {
		return b.Title
	}
	return filepath.Base(b.FilePath)
}

// ---- helpers ----

// fillRect paints a solid w×h rectangle with its top-left corner at (x, y).
func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Src)
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

func (r *Renderer) drawWiFiIcon(x, y int, connected bool) {
	col := colorWifiOn
	if !connected {
		col = colorWifiOff
	}
	fillRect(r.canvas, x, y+6, 2, 4, col)
	fillRect(r.canvas, x+4, y+3, 2, 7, col)
	fillRect(r.canvas, x+8, y, 2, 10, col)
	if !connected {
		fillRect(r.canvas, x-1, y+4, 12, 2, colorWifiOff)
		fillRect(r.canvas, x+4, y-1, 2, 12, colorWifiOff)
	}
}

// copyToRGB565 converts the RGBA canvas to RGB565 and writes it to the mmap.
func copyToRGB565(dst []byte, src *image.RGBA) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	offset := 0
	for y := range h {
		srcOffset := src.PixOffset(0, y)
		for range w {
			r := src.Pix[srcOffset]
			g := src.Pix[srcOffset+1]
			b := src.Pix[srcOffset+2]
			srcOffset += 4

			r5 := uint16(r) >> 3
			g6 := uint16(g) >> 2
			b5 := uint16(b) >> 3
			rgb565 := (r5 << 11) | (g6 << 5) | b5

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
	r.requestRender()
}
