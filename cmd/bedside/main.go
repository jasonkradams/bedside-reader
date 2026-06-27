package main

import (
	"image"
	"image/color"
	"image/draw"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/coreos/go-systemd/v22/daemon"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func main() {
	log.Println("Starting Bedside Audiobook Appliance (Native Framebuffer Mode)...")

	// Open the framebuffer device
	fbFile, err := os.OpenFile("/dev/fb1", os.O_RDWR, 0)
	if err != nil {
		log.Fatalf("Failed to open /dev/fb1: %v", err)
	}
	defer fbFile.Close()

	// Memory map the framebuffer (320x240, 16-bit color = 153600 bytes)
	fbSize := 320 * 240 * 2
	buffer := make([]byte, fbSize)

	// Create an in-memory canvas
	canvas := image.NewRGBA(image.Rect(0, 0, 320, 240))

	// Draw a dark blue background
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{color.RGBA{0, 0, 50, 255}}, image.Point{}, draw.Src)

	// Draw text
	addLabel(canvas, 100, 120, "Bedside Native UI", color.RGBA{255, 255, 255, 255})

	// Copy the canvas to the RGB565 byte buffer
	copyToRGB565(buffer, canvas)

	// Write directly to the framebuffer (forces kernel to flush to SPI)
	if _, err := fbFile.Seek(0, 0); err != nil {
		log.Printf("Failed to seek fb: %v", err)
	}
	if _, err := fbFile.Write(buffer); err != nil {
		log.Printf("Failed to write to fb: %v", err)
	}

	// Notify systemd that the service is ready
	sent, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		log.Printf("Failed to notify systemd: %v", err)
	} else if sent {
		log.Println("Systemd notified of readiness.")
	}

	// Wait for termination signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down Bedside...")
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
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
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
