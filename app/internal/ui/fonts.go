package ui

import (
	"embed"
	"image"
	"image/color"
	"log"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

//go:embed assets/fonts/*.ttf
var fontFS embed.FS

// FontChoice is a selectable typeface: a regular weight plus a heavier weight
// used for titles. The ID is the stable key persisted in system state; Name is
// what the settings menu shows.
type FontChoice struct {
	ID      string
	Name    string
	regular string
	bold    string
}

// fontRegistry is the ordered ring the settings menu cycles through. Add a TTF
// to assets/fonts/ and an entry here to offer another typeface — no other change
// is needed (the face cache and settings cycle pick it up automatically).
var fontRegistry = []FontChoice{
	{ID: "plex-serif", Name: "IBM Plex Serif", regular: "IBMPlexSerif-Regular.ttf", bold: "IBMPlexSerif-SemiBold.ttf"},
	{ID: "crimson", Name: "Crimson Text", regular: "CrimsonText-Regular.ttf", bold: "CrimsonText-SemiBold.ttf"},
	{ID: "zilla-slab", Name: "Zilla Slab", regular: "ZillaSlab-Regular.ttf", bold: "ZillaSlab-SemiBold.ttf"},
	{ID: "atkinson", Name: "Atkinson Hyperlegible", regular: "AtkinsonHyperlegible-Regular.ttf", bold: "AtkinsonHyperlegible-Bold.ttf"},
	{ID: "space-mono", Name: "Space Mono", regular: "SpaceMono-Regular.ttf", bold: "SpaceMono-Bold.ttf"},
}

const defaultFontID = "plex-serif"

// Text sizes (px; the face cache renders at DPI 72 so 1pt == 1px). Tuned for the
// 320x240 panel; adjust here and every screen follows.
const (
	sizeTitle   = 18 // smaller so long titles fit the narrow column in more lines
	sizeChapter = 15
	sizeBody    = 14
	sizeSmall   = 12
	sizeMenuHdr = 17
	sizeMenuRow = 15
)

// fontByID returns the registered FontChoice for id, falling back to the default.
func fontByID(id string) FontChoice {
	for _, f := range fontRegistry {
		if f.ID == id {
			return f
		}
	}
	return fontByID(defaultFontID)
}

// NextFontID returns the next font in the registry ring after id, wrapping around.
func NextFontID(id string) string {
	for i, f := range fontRegistry {
		if f.ID == id {
			return fontRegistry[(i+1)%len(fontRegistry)].ID
		}
	}
	return defaultFontID
}

type faceKey struct {
	file string
	size float64
}

// fontManager parses the embedded TTFs and caches rasterized faces. Parsing and
// face construction are relatively expensive, so both are memoized; a face is
// built once per (file, size) and reused for every draw.
type fontManager struct {
	mu     sync.Mutex
	parsed map[string]*sfnt.Font
	faces  map[faceKey]font.Face
}

func newFontManager() *fontManager {
	return &fontManager{
		parsed: map[string]*sfnt.Font{},
		faces:  map[faceKey]font.Face{},
	}
}

func (fm *fontManager) sfntFor(file string) (*sfnt.Font, error) {
	if f, ok := fm.parsed[file]; ok {
		return f, nil
	}
	data, err := fontFS.ReadFile("assets/fonts/" + file)
	if err != nil {
		return nil, err
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, err
	}
	fm.parsed[file] = f
	return f, nil
}

// face returns a cached font.Face for the given embedded TTF at size px. On any
// failure it falls back to the built-in bitmap face so text still renders.
func (fm *fontManager) face(file string, size float64) font.Face {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	key := faceKey{file, size}
	if fc, ok := fm.faces[key]; ok {
		return fc
	}

	sf, err := fm.sfntFor(file)
	if err != nil {
		log.Printf("font: parse %s failed: %v (using fallback)", file, err)
		fm.faces[key] = basicfont.Face7x13
		return basicfont.Face7x13
	}
	fc, err := opentype.NewFace(sf, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		log.Printf("font: face %s@%.0f failed: %v (using fallback)", file, size, err)
		fm.faces[key] = basicfont.Face7x13
		return basicfont.Face7x13
	}
	fm.faces[key] = fc
	return fc
}

// drawText draws s with its baseline at (x, y) in colour col.
func drawText(dst *image.RGBA, x, y int, s string, face font.Face, col color.RGBA) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(s)
}

// textWidth returns the rendered pixel width of s in face.
func textWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Round()
}

// drawRightText draws s so it ends at x (baseline y).
func drawRightText(dst *image.RGBA, x, y int, s string, face font.Face, col color.RGBA) {
	drawText(dst, x-textWidth(face, s), y, s, face, col)
}

// lineHeight returns a sensible line advance (ascent+descent) for face, in px.
func lineHeight(face font.Face) int {
	m := face.Metrics()
	return (m.Ascent + m.Descent).Round()
}

// ellipsize shortens s so it renders within maxWidth px in face, appending "…"
// when it must cut. Operates on runes so it never splits a multi-byte rune.
func ellipsize(face font.Face, s string, maxWidth int) string {
	if textWidth(face, s) <= maxWidth {
		return s
	}
	const ell = "…"
	ellW := textWidth(face, ell)
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if textWidth(face, string(runes))+ellW <= maxWidth {
			return string(runes) + ell
		}
	}
	return ell
}

// wrapLines greedily word-wraps s to at most maxLines lines that each fit in
// maxWidth px. A single word wider than maxWidth is placed alone and ellipsized;
// if the text needs more than maxLines, the last kept line is ellipsized.
func wrapLines(face font.Face, s string, maxWidth, maxLines int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		cand := w
		if cur != "" {
			cand = cur + " " + w
		}
		if cur == "" || textWidth(face, cand) <= maxWidth {
			cur = cand
			continue
		}
		lines = append(lines, cur)
		cur = w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	for i := range lines {
		lines[i] = ellipsize(face, lines[i], maxWidth)
	}
	return lines
}
