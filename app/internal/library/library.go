package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jasonkradams/bedside-reader/internal/bus"
	"go.etcd.io/bbolt"
)

var (
	bucketLibrary  = []byte("library")
	bucketProgress = []byte("progress")
	bucketSystem   = []byte("system")
)

// Audiobook represents the parsed metadata of a single file
type Audiobook struct {
	ID        string    `json:"id"`
	FilePath  string    `json:"file_path"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	Duration  float64   `json:"duration"`
	CoverHash string    `json:"cover_hash"` // used to locate cover art image
	Chapters  []Chapter `json:"chapters"`
}

// Chapter represents a single chapter in an audiobook
type Chapter struct {
	ID        int     `json:"id"`
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

// Progress represents the saved playback state of an audiobook
type Progress struct {
	Position   float64 `json:"position"`
	ChapterIdx int     `json:"chapter_idx"`
}

// Manager handles the database and scanning
type Manager struct {
	db        *bbolt.DB
	bus       *bus.Bus
	audioDir  string
	coverDir  string
	stop      chan struct{}
	closeOnce sync.Once
}

func New(eventBus *bus.Bus, dbPath, audioDir, coverDir string) (*Manager, error) {
	// Ensure directories exist
	if err := os.MkdirAll(audioDir, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(coverDir, 0755); err != nil {
		return nil, err
	}

	db, err := bbolt.Open(dbPath, 0666, nil)
	if err != nil {
		return nil, err
	}

	// Create buckets if they don't exist
	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketLibrary)
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists(bucketProgress)
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists(bucketSystem)
		return err
	})
	if err != nil {
		return nil, err
	}

	m := &Manager{
		db:       db,
		bus:      eventBus,
		audioDir: audioDir,
		coverDir: coverDir,
		stop:     make(chan struct{}),
	}

	return m, nil
}

// Close stops the directory watcher and closes the underlying boltdb.
func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.stop) })
	m.db.Close()
}

// Watch runs an initial scan, then rescans whenever the audiobook directory
// changes, instead of polling. Uploads emit a burst of write events, so scans
// are debounced until the directory goes quiet. Runs until Close.
func (m *Manager) Watch() {
	m.Scan() // initial pass (also backfills cover art)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("library: watcher unavailable, covers update only at startup: %v", err)
		return
	}
	defer w.Close()
	if err := w.Add(m.audioDir); err != nil {
		log.Printf("library: watch %s failed: %v", m.audioDir, err)
		return
	}

	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	for {
		select {
		case <-m.stop:
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if isAudioEvent(ev) {
				debounce.Reset(5 * time.Second)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("library: watch error: %v", err)
		case <-debounce.C:
			m.Scan()
		}
	}
}

// isAudioEvent reports whether ev is a create/write/rename/remove of an
// audiobook file worth rescanning for.
func isAudioEvent(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	switch strings.ToLower(filepath.Ext(ev.Name)) {
	case ".m4b", ".mp3", ".m4a":
		return true
	}
	return false
}

// Scan crawls the audio directory and uses ffprobe to parse metadata
func (m *Manager) Scan() {
	m.bus.Publish(bus.EventLibraryScanStarted, nil)
	defer m.bus.Publish(bus.EventLibraryScanComplete, nil)

	entries, err := os.ReadDir(m.audioDir)
	if err != nil {
		log.Printf("Scan error reading dir: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue // Flat directory structure for now
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".m4b" && ext != ".mp3" && ext != ".m4a" {
			continue
		}

		path := filepath.Join(m.audioDir, entry.Name())
		m.processFile(path)
	}
}

func (m *Manager) processFile(path string) {
	id := idForPath(path)

	book, exists := m.getByID(id)
	if !exists {
		log.Printf("Scanning new file: %s", filepath.Base(path))
		probed, err := m.probeFile(path, id)
		if err != nil {
			log.Printf("Failed to probe file %s: %v", path, err)
			return
		}
		book = probed
	}

	// Extract cover art on first scan, and backfill it for books catalogued
	// before cover support existed (whose cached image is missing).
	changed := m.ensureCover(book)

	if !exists || changed {
		m.save(book)
	}
}

// idForPath derives the stable library ID from a file's base name.
func idForPath(path string) string {
	hash := sha256.Sum256([]byte(filepath.Base(path)))
	return hex.EncodeToString(hash[:12])
}

// getByID returns the stored book for id and whether it was present.
func (m *Manager) getByID(id string) (*Audiobook, bool) {
	var book *Audiobook
	_ = m.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucketLibrary).Get([]byte(id))
		if v == nil {
			return nil
		}
		var b Audiobook
		if err := json.Unmarshal(v, &b); err != nil {
			return nil
		}
		book = &b
		return nil
	})
	return book, book != nil
}

func (m *Manager) save(book *Audiobook) {
	_ = m.db.Update(func(tx *bbolt.Tx) error {
		data, _ := json.Marshal(book)
		return tx.Bucket(bucketLibrary).Put([]byte(book.ID), data)
	})
}

// CoverPath returns the on-disk path where a book's cover art is (or would be)
// cached, or "" for an empty id.
func (m *Manager) CoverPath(id string) string {
	if id == "" {
		return ""
	}
	return filepath.Join(m.coverDir, id+".jpg")
}

// ensureCover extracts the embedded cover art to disk if it isn't cached yet and
// keeps CoverHash in sync (set to the book ID when art exists, "" otherwise).
// Returns true when the book record changed and needs saving.
func (m *Manager) ensureCover(book *Audiobook) bool {
	coverPath := m.CoverPath(book.ID)
	if _, err := os.Stat(coverPath); err == nil {
		if book.CoverHash != book.ID {
			book.CoverHash = book.ID
			return true
		}
		return false
	}

	if err := extractCover(book.FilePath, coverPath); err != nil {
		if book.CoverHash != "" {
			book.CoverHash = ""
			return true
		}
		return false
	}
	if book.CoverHash != book.ID {
		book.CoverHash = book.ID
	}
	return true
}

// extractCover writes a downscaled JPEG of the audiobook's embedded cover art to
// dst via ffmpeg. Returns a non-nil error when the file has no attached picture.
func extractCover(src, dst string) error {
	err := execContained(30*time.Second, nil, "ffmpeg",
		"-y", "-v", "error",
		"-i", src,
		"-an",
		"-map", "0:v:0",
		"-frames:v", "1",
		"-vf", "scale=256:256:force_original_aspect_ratio=decrease",
		"-q:v", "3",
		dst,
	)
	if err != nil {
		os.Remove(dst) // don't leave a partial/zero-byte file behind
		return fmt.Errorf("ffmpeg cover extract: %w", err)
	}
	return nil
}

// execContained runs name+args with a hard timeout and at the nicest CPU / idle
// I/O priority, so metadata probing and cover extraction can never hang the
// scanner or steal cycles from playback. If stdout is non-nil it captures the
// command's stdout.
func execContained(timeout time.Duration, stdout *bytes.Buffer, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	lowerPriority(cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s timed out after %s", name, timeout)
		}
		return err
	}
	return nil
}

// probeFile runs ffprobe to extract metadata and chapters from an audiobook file.
func (m *Manager) probeFile(path, id string) (*Audiobook, error) {
	var out bytes.Buffer
	if err := execContained(60*time.Second, &out, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_chapters",
		"-show_streams",
		path,
	); err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var result struct {
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
		Chapters []struct {
			ID        int               `json:"id"`
			StartTime float64           `json:"start_time,string"`
			EndTime   float64           `json:"end_time,string"`
			Tags      map[string]string `json:"tags"`
		} `json:"chapters"`
	}

	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe json: %w", err)
	}

	var duration float64
	_, _ = fmt.Sscanf(result.Format.Duration, "%f", &duration)

	book := &Audiobook{
		ID:       id,
		FilePath: path,
		Title:    result.Format.Tags["title"],
		Author:   result.Format.Tags["artist"],
		Duration: duration,
	}

	if book.Title == "" {
		book.Title = filepath.Base(path)
	}

	for _, c := range result.Chapters {
		book.Chapters = append(book.Chapters, Chapter{
			ID:        c.ID,
			Title:     c.Tags["title"],
			StartTime: c.StartTime,
			EndTime:   c.EndTime,
		})
	}

	return book, nil
}

// GetAll returns all audiobooks in the library
func (m *Manager) GetAll() ([]Audiobook, error) {
	var books []Audiobook
	err := m.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketLibrary)
		return b.ForEach(func(k, v []byte) error {
			var book Audiobook
			if err := json.Unmarshal(v, &book); err != nil {
				return err
			}
			books = append(books, book)
			return nil
		})
	})
	return books, err
}

// GetByFilename finds an audiobook by its base filename
func (m *Manager) GetByFilename(filename string) (*Audiobook, error) {
	var found *Audiobook
	err := m.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketLibrary)
		return b.ForEach(func(k, v []byte) error {
			var book Audiobook
			if err := json.Unmarshal(v, &book); err != nil {
				return err
			}
			if filepath.Base(book.FilePath) == filename {
				found = &book
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("book not found")
	}
	return found, nil
}

// ChapterIndexAt returns the index of the chapter that contains the given position.
// It includes a 0.5s tolerance for chapter boundaries.
func ChapterIndexAt(chapters []Chapter, position float64) int {
	idx := -1
	for i, chap := range chapters {
		if position >= chap.StartTime-0.5 {
			idx = i
		} else {
			break
		}
	}
	return idx
}

// SaveProgress saves the playback position for a specific file
func (m *Manager) SaveProgress(filename string, position float64) error {
	return m.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketProgress)
		val := fmt.Sprintf("%f", position)
		return b.Put([]byte(filename), []byte(val))
	})
}

// GetProgress retrieves the playback position for a specific file
func (m *Manager) GetProgress(filename string) (float64, error) {
	var position float64
	err := m.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketProgress)
		val := b.Get([]byte(filename))
		if val != nil {
			fmt.Sscanf(string(val), "%f", &position)
		}
		return nil
	})
	return position, err
}

type SystemState struct {
	ActiveFile  string  `json:"activeFile"`
	Playing     bool    `json:"playing"`
	Timeout     int     `json:"timeout"`
	Volume      float64 `json:"volume"`
	EncoderMode string  `json:"encoderMode"`
	Font        string  `json:"font"` // UI typeface ID (see internal/ui font registry)
}

// SaveSystemState saves the full system state
func (m *Manager) SaveSystemState(state SystemState) error {
	return m.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSystem)
		data, _ := json.Marshal(state)
		return b.Put([]byte("system_state"), data)
	})
}

// GetSystemState retrieves the full system state
func (m *Manager) GetSystemState() (SystemState, error) {
	state := SystemState{
		Timeout:     5,            // Default to 5 minutes
		Volume:      50,           // Default volume
		EncoderMode: "vol",        // Default to volume mode
		Font:        "plex-serif", // mirrors ui.defaultFontID
	}
	err := m.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSystem)
		val := b.Get([]byte("system_state"))
		if val != nil {
			_ = json.Unmarshal(val, &state)
		}
		return nil
	})
	return state, err
}
