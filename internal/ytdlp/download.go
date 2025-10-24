package ytdlp

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/NamanBalaji/tdm/internal/status"
)

// Download represents a yt-dlp managed download.
type Download struct {
	mu sync.RWMutex

	ID   uuid.UUID `json:"id"`
	URL  string    `json:"url"`
	Dir  string    `json:"dir"`
	Path string    `json:"path"`

	Status     status.Status `json:"status"`
	Priority   int           `json:"priority"`
	TotalSize  int64         `json:"totalSize"`
	Downloaded int64         `json:"downloaded"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// NewDownload creates a new yt-dlp download metadata entry.
func NewDownload(url, dir string, priority int) *Download {
	now := time.Now()

	return &Download{
		ID:        uuid.New(),
		URL:       url,
		Dir:       dir,
		Status:    status.Pending,
		Priority:  priority,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// MarshalJSON implements json.Marshaler ensuring atomic fields are captured safely.
func (d *Download) MarshalJSON() ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	type Alias Download

	return json.Marshal(&struct {
		*Alias

		Status     int32 `json:"status"`
		TotalSize  int64 `json:"totalSize"`
		Downloaded int64 `json:"downloaded"`
	}{
		Alias:      (*Alias)(d),
		Status:     d.getStatus(),
		TotalSize:  d.getTotalSize(),
		Downloaded: d.getDownloaded(),
	})
}

// Type returns the repository type label for yt-dlp downloads.
func (d *Download) Type() string {
	return "ytdlp"
}

// GetID returns the download identifier.
func (d *Download) GetID() uuid.UUID {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.ID
}

func (d *Download) getURL() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.URL
}

func (d *Download) getDir() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.Dir
}

func (d *Download) setPath(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.Path = filepath.Clean(path)
	d.touchLocked()
}

func (d *Download) getPath() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.Path
}

func (d *Download) getFilename() string {
	path := d.getPath()
	if path == "" {
		return ""
	}

	return filepath.Base(path)
}

func (d *Download) setStatus(s status.Status) {
	atomic.StoreInt32((*int32)(&d.Status), s)
	d.touch()
}

func (d *Download) getStatus() status.Status {
	return atomic.LoadInt32((*int32)(&d.Status))
}

func (d *Download) setPriority(priority int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.Priority = priority
	d.touchLocked()
}

func (d *Download) getPriority() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.Priority
}

func (d *Download) setProgress(downloaded, total int64) {
	atomic.StoreInt64(&d.Downloaded, downloaded)
	atomic.StoreInt64(&d.TotalSize, total)
	d.touch()
}

func (d *Download) getDownloaded() int64 {
	return atomic.LoadInt64(&d.Downloaded)
}

func (d *Download) getTotalSize() int64 {
	return atomic.LoadInt64(&d.TotalSize)
}

func (d *Download) touch() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.touchLocked()
}

func (d *Download) touchLocked() {
	d.UpdatedAt = time.Now()
}
