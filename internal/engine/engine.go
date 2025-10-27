package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/NamanBalaji/tdm/internal/config"
	"github.com/NamanBalaji/tdm/internal/logger"
	"github.com/NamanBalaji/tdm/internal/progress"
	"github.com/NamanBalaji/tdm/internal/repository"
	"github.com/NamanBalaji/tdm/internal/status"
	"github.com/NamanBalaji/tdm/internal/worker"
	"github.com/NamanBalaji/tdm/internal/ytdlp"
	torrentPkg "github.com/NamanBalaji/tdm/pkg/torrent"
)

var (
	ErrWorkerNotFound       = errors.New("worker not found")
	ErrInvalidPriority      = errors.New("priority must be between 1 and 10")
	ErrDownloadPauseTimeout = errors.New("timeout waiting for downloads to pause")
)

// DownloadError represents an error for a specific download.
type DownloadError struct {
	ID    uuid.UUID
	Error error
}

// Engine manages multiple download workers.
type Engine struct {
	mu            sync.RWMutex
	cfg           *config.Config
	torrentClient *torrentPkg.Client
	repo          *repository.BboltRepository
	workers       map[uuid.UUID]worker.Worker
	queue         *PriorityQueue
	shutdownOnce  sync.Once
	shutdownDone  chan struct{}
	errors        chan DownloadError
}

// NewEngine creates a new download engine.
func NewEngine(cfg *config.Config, repo *repository.BboltRepository, torrentClient *torrentPkg.Client) *Engine {
	return &Engine{
		cfg:           cfg,
		repo:          repo,
		torrentClient: torrentClient,
		workers:       make(map[uuid.UUID]worker.Worker),
		queue:         NewPriorityQueue(cfg.MaxConcurrentDownloads),
		shutdownDone:  make(chan struct{}),
		errors:        make(chan DownloadError, 3),
	}
}

// Start initializes the engine and loads existing downloads.
func (e *Engine) Start(ctx context.Context) error {
	downloads, err := e.repo.GetAll()
	if err != nil {
		return fmt.Errorf("failed to load downloads: %w", err)
	}

	for _, dl := range downloads {
		w, err := worker.LoadWorker(ctx, e.cfg, dl, e.torrentClient, e.repo)
		if err != nil {
			logger.Errorf("Failed to load worker for download: %v", err)
			continue
		}

		e.addWorker(w)

		go e.monitorWorker(ctx, w)
	}

	return nil
}

// AddDownload adds a new download to the engine.
func (e *Engine) AddDownload(ctx context.Context, url string, priority int, format string) uuid.UUID {
	if priority < 1 || priority > 10 {
		e.errors <- DownloadError{ID: uuid.Nil, Error: ErrInvalidPriority}

		return uuid.Nil
	}

	w, err := worker.GetWorker(ctx, e.cfg, url, priority, e.torrentClient, e.repo, format)
	if err != nil {
		e.errors <- DownloadError{ID: uuid.Nil, Error: err}

		return uuid.Nil
	}

	id := w.GetID()

	e.mu.Lock()
	e.workers[id] = w
	e.queue.Add(ctx, w)
	e.mu.Unlock()

	go e.monitorWorker(ctx, w)

	return id
}

// ListFormats returns the available formats for a supported media URL.
func (e *Engine) ListFormats(ctx context.Context, url string) ([]ytdlp.Format, error) {
	if e.cfg.Ytdlp == nil {
		return nil, fmt.Errorf("yt-dlp integration is not configured")
	}

	if !ytdlp.CanHandle(url) {
		return nil, fmt.Errorf("format selection is available only for supported media URLs")
	}

	return ytdlp.ListFormats(ctx, e.cfg.Ytdlp, url)
}

// PauseDownload pauses a download.
func (e *Engine) PauseDownload(ctx context.Context, id uuid.UUID) {
	e.mu.RLock()
	w, exists := e.workers[id]
	e.mu.RUnlock()

	if !exists {
		return
	}

	err := w.Pause()
	if err != nil {
		e.errors <- DownloadError{ID: id, Error: fmt.Errorf("failed to pause download: %w", err)}
	}

	e.mu.Lock()
	e.queue.Remove(ctx, w)
	e.mu.Unlock()
}

// ResumeDownload resumes a paused download.
func (e *Engine) ResumeDownload(ctx context.Context, id uuid.UUID) {
	e.mu.RLock()
	w, exists := e.workers[id]
	e.mu.RUnlock()

	if !exists {
		return
	}

	s := w.GetStatus()
	if s != status.Paused && s != status.Failed {
		return
	}

	e.mu.Lock()
	e.queue.Add(ctx, w)
	e.mu.Unlock()
}

// CancelDownload cancels a download.
func (e *Engine) CancelDownload(ctx context.Context, id uuid.UUID) {
	e.mu.RLock()
	w, exists := e.workers[id]
	e.mu.RUnlock()

	if !exists {
		return
	}

	err := w.Cancel()
	if err != nil {
		e.errors <- DownloadError{ID: id, Error: fmt.Errorf("failed to cancel download: %w", err)}

		return
	}

	e.mu.Lock()
	e.queue.Remove(ctx, w)
	e.mu.Unlock()
}

// RemoveDownload removes a download completely.
func (e *Engine) RemoveDownload(ctx context.Context, id uuid.UUID) {
	e.mu.Lock()

	w, exists := e.workers[id]
	if !exists {
		e.mu.Unlock()
		return
	}

	delete(e.workers, id)
	e.queue.Remove(ctx, w)

	e.mu.Unlock()

	err := w.Remove()
	if err != nil {
		e.errors <- DownloadError{ID: id, Error: fmt.Errorf("failed to remove download: %w", err)}
	}
}

// GetProgress returns the progress of a download.
func (e *Engine) GetProgress(id uuid.UUID) (progress.Progress, error) {
	e.mu.RLock()
	w, exists := e.workers[id]
	e.mu.RUnlock()

	if !exists {
		return progress.Progress{}, ErrWorkerNotFound
	}

	return w.Progress(), nil
}

// GetAllDownloads returns info about all downloads.
func (e *Engine) GetAllDownloads() []DownloadInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	downloads := make([]DownloadInfo, 0, len(e.workers))
	for id, w := range e.workers {
		downloads = append(downloads, DownloadInfo{
			ID:       id,
			Filename: w.GetFilename(),
			Status:   w.GetStatus(),
			Priority: w.GetPriority(),
			Progress: w.Progress(),
		})
	}

	sort.Slice(downloads, func(i, j int) bool {
		if downloads[i].Priority != downloads[j].Priority {
			return downloads[i].Priority > downloads[j].Priority
		}

		return downloads[i].ID.String() < downloads[j].ID.String()
	})

	return downloads
}

// GetErrors returns the error channel for monitoring download errors.
func (e *Engine) GetErrors() <-chan DownloadError {
	return e.errors
}

// Shutdown gracefully shuts down the engine.
func (e *Engine) Shutdown(ctx context.Context) error {
	var shutdownErr error

	e.shutdownOnce.Do(func() {
		e.mu.Lock()

		var wg sync.WaitGroup

		for _, w := range e.workers {
			if w.GetStatus() == status.Active {
				wg.Add(1)

				go func(worker worker.Worker) {
					defer wg.Done()

					err := worker.Pause()
					if err != nil {
						logger.Errorf("Failed to pause download %s: %v", worker.GetID(), err)
					}
				}(w)
			}
		}

		e.mu.Unlock()

		done := make(chan struct{})

		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-ctx.Done():
			shutdownErr = ctx.Err()
		case <-time.After(10 * time.Second):
			shutdownErr = ErrDownloadPauseTimeout
		}

		close(e.errors)
		close(e.shutdownDone)
	})

	return shutdownErr
}

// Wait waits for the engine to shut down.
func (e *Engine) Wait() {
	<-e.shutdownDone
}

// addWorker adds a worker to the engine.
func (e *Engine) addWorker(w worker.Worker) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.workers[w.GetID()] = w
}

// monitorWorker monitors a single worker for completion.
func (e *Engine) monitorWorker(ctx context.Context, w worker.Worker) {
	select {
	case err := <-w.Done():
		e.handleWorkerDone(ctx, w.GetID(), err)
	case <-e.shutdownDone:
		return
	case <-ctx.Done():
		return
	}
}

// handleWorkerDone handles cleanup when a worker finishes.
func (e *Engine) handleWorkerDone(ctx context.Context, workerID uuid.UUID, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	w, exists := e.workers[workerID]
	if !exists {
		return
	}

	s := w.GetStatus()

	if s == status.Completed || s == status.Failed || s == status.Cancelled {
		e.queue.Remove(ctx, w)

		if err != nil {
			select {
			case e.errors <- DownloadError{ID: workerID, Error: err}:
			default:
				logger.Errorf("Download %s failed: %v", workerID, err)
			}
		}
	}
}

// DownloadInfo contains information about a download.
type DownloadInfo struct {
	ID       uuid.UUID
	Filename string
	Status   status.Status
	Priority int
	Progress progress.Progress
}
