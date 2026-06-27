package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	db       *bbolt.DB
	bus      *bus.Bus
	audioDir string
	coverDir string
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
	}

	// Listen for progress ticks to save state
	go m.listenProgress()

	return m, nil
}

func (m *Manager) listenProgress() {
	ch := m.bus.Subscribe()
	for ev := range ch {
		if ev.Type == bus.EventPlayerProgressTick {
			// Save progress occasionally or on pause/stop?
			// For a Pi Zero 2 with an SD card, we probably shouldn't fsync 1Hz ticks.
			// The architecture doc says: "written every SSE tick (~1Hz)".
			// We will write it but maybe batch it if IO is an issue.
			// For now, write synchronously.
			// TODO: Actually parse Payload and save.
		}
	}
}

// Close closes the underlying boltdb
func (m *Manager) Close() {
	m.db.Close()
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
	// Generate an ID by hashing the filename
	hash := sha256.Sum256([]byte(filepath.Base(path)))
	id := hex.EncodeToString(hash[:12])

	// Check if already in DB
	exists := false
	m.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketLibrary)
		if b.Get([]byte(id)) != nil {
			exists = true
		}
		return nil
	})

	if exists {
		return // Already scanned
	}

	log.Printf("Scanning new file: %s", filepath.Base(path))

	// Run ffprobe
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_chapters",
		"-show_streams",
		path,
	)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		log.Printf("ffprobe failed on %s: %v", path, err)
		return
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
		log.Printf("failed to parse ffprobe json: %v", err)
		return
	}

	// Parse duration safely
	var duration float64
	fmt.Sscanf(result.Format.Duration, "%f", &duration)

	book := Audiobook{
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

	// Save to DB
	m.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketLibrary)
		data, _ := json.Marshal(book)
		return b.Put([]byte(id), data)
	})
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

// SaveSystemState saves the last active file and whether it was playing
func (m *Manager) SaveSystemState(activeFile string, playing bool) error {
	return m.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSystem)
		state := map[string]interface{}{
			"activeFile": activeFile,
			"playing":    playing,
		}
		data, _ := json.Marshal(state)
		return b.Put([]byte("system_state"), data)
	})
}

// GetSystemState retrieves the last active file and whether it was playing
func (m *Manager) GetSystemState() (string, bool, error) {
	var activeFile string
	var playing bool
	err := m.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSystem)
		val := b.Get([]byte("system_state"))
		if val != nil {
			var state map[string]interface{}
			if err := json.Unmarshal(val, &state); err == nil {
				if af, ok := state["activeFile"].(string); ok {
					activeFile = af
				}
				if pl, ok := state["playing"].(bool); ok {
					playing = pl
				}
			}
		}
		return nil
	})
	return activeFile, playing, err
}
