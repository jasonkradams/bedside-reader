package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"os"

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
		}

		if needsRender {
			r.render()
		}
	}
}

func (r *Renderer) render() {
	// 1. Clear background (dark blue)
	draw.Draw(r.canvas, r.canvas.Bounds(), &image.Uniform{color.RGBA{0, 0, 50, 255}}, image.Point{}, draw.Src)

	// 2. Draw Title
	title := r.playState.FilePath
	if title == "" {
		title = "Bedside Audio"
	}
	addLabel(r.canvas, 10, 30, title, color.RGBA{255, 255, 255, 255})

	// 3. Draw State
	status := "Paused"
	if title == "Bedside Audio" {
		status = "Idle"
	} else if !r.playState.Paused {
		status = "Playing"
	}
	addLabel(r.canvas, 10, 100, status, color.RGBA{150, 255, 150, 255})

	// 4. Draw Time
	timeStr := fmt.Sprintf("%02d:%02d / %02d:%02d",
		int(r.playState.Position)/60, int(r.playState.Position)%60,
		int(r.playState.Duration)/60, int(r.playState.Duration)%60,
	)
	addLabel(r.canvas, 10, 140, timeStr, color.RGBA{200, 200, 200, 255})

	// Copy to hardware
	copyToRGB565(r.mmap, r.canvas)
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

func copyToRGB565(fb []byte, img *image.RGBA) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	i := 0
	for y := range height {
		for x := range width {
			c := img.RGBAAt(x, y)

			// RGB565 conversion
			r := uint16(c.R) >> 3
			g := uint16(c.G) >> 2
			b := uint16(c.B) >> 3

			rgb565 := (r << 11) | (g << 5) | b

			// Little Endian layout
			fb[i] = byte(rgb565)
			fb[i+1] = byte(rgb565 >> 8)
			i += 2
		}
	}
}
