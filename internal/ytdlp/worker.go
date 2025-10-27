package ytdlp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/NamanBalaji/tdm/internal/config"
	"github.com/NamanBalaji/tdm/internal/logger"
	"github.com/NamanBalaji/tdm/internal/progress"
	"github.com/NamanBalaji/tdm/internal/repository"
	"github.com/NamanBalaji/tdm/internal/status"
)

var (
	// ErrAlreadyStarted is returned when Start is invoked twice without resetting.
	ErrAlreadyStarted = errors.New("download already started")
	// ErrBinaryNotFound indicates the configured yt-dlp binary was not found.
	ErrBinaryNotFound = errors.New("yt-dlp binary not found")
)

// Worker manages a single yt-dlp download process.
type Worker struct {
	cfg      *config.YTDLPConfig
	repo     *repository.BboltRepository
	download *Download

	done     chan error
	started  atomic.Bool
	finished atomic.Bool
	skipSave atomic.Bool

	cancel   context.CancelFunc
	cancelMu sync.Mutex

	progressMu   sync.RWMutex
	lastProgress progress.Progress
}

// Options control additional behaviour for yt-dlp workers.
type Options struct {
	// Format specifies the yt-dlp format identifier to download.
	Format string
}

// New creates a new yt-dlp worker.
func New(_ context.Context, cfg *config.YTDLPConfig, url string, downloadData *Download, repo *repository.BboltRepository, priority int, opts *Options) (*Worker, error) {
	if cfg == nil {
		return nil, fmt.Errorf("ytdlp config is required")
	}

	var download *Download
	if downloadData != nil {
		downloadData.mu = sync.RWMutex{}
		if downloadData.Status == status.Active {
			downloadData.setStatus(status.Paused)
		}
		if downloadData.getDir() == "" {
			downloadData.Dir = cfg.DownloadDir
		}
		if opts != nil && opts.Format != "" {
			downloadData.setFormat(opts.Format)
		}
		download = downloadData
	} else {
		dir := cfg.DownloadDir
		if dir == "" {
			dir = os.TempDir()
		}

		format := ""
		if opts != nil {
			format = opts.Format
		}

		download = NewDownload(url, dir, priority, format)
	}

	if download.getPriority() != priority {
		download.setPriority(priority)
	}

	percentage := calculatePercentage(download.getDownloaded(), download.getTotalSize())
	lastProgress := progress.Progress{
		TotalSize:  download.getTotalSize(),
		Downloaded: download.getDownloaded(),
		Percentage: percentage,
		SpeedBPS:   0,
		ETA:        0,
	}

	worker := &Worker{
		cfg:          cfg,
		repo:         repo,
		download:     download,
		done:         make(chan error, 1),
		lastProgress: lastProgress,
	}

	if repo != nil {
		if err := repo.Save(download); err != nil {
			return nil, fmt.Errorf("failed to save download: %w", err)
		}
	}

	return worker, nil
}

// CanHandle determines whether the provided URL should be handled by yt-dlp.
func CanHandle(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")

	switch {
	case host == "youtu.be":
		return true
	case strings.HasSuffix(host, "youtube.com"):
		return true
	case strings.HasSuffix(host, "youtube-nocookie.com"):
		return true
	default:
		return false
	}
}

// GetPriority returns the worker priority.
func (w *Worker) GetPriority() int {
	return w.download.getPriority()
}

// GetID returns the download ID.
func (w *Worker) GetID() uuid.UUID {
	return w.download.GetID()
}

// GetStatus returns the worker status.
func (w *Worker) GetStatus() status.Status {
	return w.download.getStatus()
}

// GetFilename returns the filename if known, otherwise the URL.
func (w *Worker) GetFilename() string {
	if name := w.download.getFilename(); name != "" {
		return name
	}

	return w.download.getURL()
}

// Start launches the yt-dlp command when the worker becomes active.
func (w *Worker) Start(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}

	w.finished.Store(false)

	currentStatus := w.download.getStatus()
	if currentStatus == status.Completed || currentStatus == status.Cancelled {
		w.started.Store(false)

		return nil
	}

	w.download.setStatus(status.Active)

	runCtx, cancel := context.WithCancel(ctx)
	w.setCancel(cancel)

	go w.saveState(runCtx)
	go func() {
		err := w.execute(runCtx)
		if errors.Is(err, context.Canceled) {
			w.finish(nil)

			return
		}

		w.finish(err)
	}()

	return nil
}

// Pause pauses the download.
func (w *Worker) Pause() error {
	return w.stop(status.Paused, false)
}

// Cancel cancels the download.
func (w *Worker) Cancel() error {
	return w.stop(status.Cancelled, false)
}

// Remove cancels the download and removes metadata/files.
func (w *Worker) Remove() error {
	return w.stop(status.Cancelled, true)
}

// Done exposes the completion channel.
func (w *Worker) Done() <-chan error {
	return w.done
}

// Progress returns the current progress snapshot.
func (w *Worker) Progress() progress.Progress {
	w.progressMu.RLock()
	defer w.progressMu.RUnlock()

	return w.lastProgress
}

// Queue marks the download as queued.
func (w *Worker) Queue() {
	w.download.setStatus(status.Queued)
}

func (w *Worker) execute(ctx context.Context) error {
	binary := w.cfg.BinaryPath
	if binary == "" {
		binary = "yt-dlp"
	}

	if _, err := exec.LookPath(binary); err != nil {
		return ErrBinaryNotFound
	}

	dir := w.download.getDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	args := []string{"--newline", "--no-playlist", "-o", filepath.Join(dir, "%(title)s.%(ext)s")}
	format := strings.TrimSpace(w.download.getFormat())
	if format == "" {
		format = strings.TrimSpace(w.cfg.Format)
	}

	if format != "" {
		args = append(args, "-f", format)
	}

	if len(w.cfg.Args) > 0 {
		args = append(args, w.cfg.Args...)
	}

	args = append(args, w.download.getURL())

	cmd := exec.CommandContext(ctx, binary, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		w.consumeOutput(stdout)
	}()

	go func() {
		defer wg.Done()
		w.consumeOutput(stderr)
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	return waitErr
}

func (w *Worker) consumeOutput(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		w.handleLine(scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		logger.Debugf("yt-dlp output error: %v", err)
	}
}

func (w *Worker) handleLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	if strings.HasPrefix(trimmed, "[download]") {
		w.handleDownloadLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "[download]")))

		return
	}

	if strings.HasPrefix(trimmed, "[Merger] Merging formats into") {
		start := strings.Index(trimmed, "\"")
		end := strings.LastIndex(trimmed, "\"")
		if start >= 0 && end > start {
			path := trimmed[start+1 : end]
			w.download.setPath(path)
		}

		return
	}

	if strings.HasPrefix(trimmed, "[Moving]") {
		segments := strings.Split(trimmed, "\"")
		if len(segments) >= 2 {
			w.download.setPath(segments[len(segments)-2])
		}
	}
}

func (w *Worker) handleDownloadLine(content string) {
	if strings.HasPrefix(content, "Destination:") {
		destination := strings.TrimSpace(strings.TrimPrefix(content, "Destination:"))
		if destination != "" {
			w.download.setPath(destination)
		}

		return
	}

	percentage, total, downloaded, speed, eta, ok := parseProgress(content)
	if !ok {
		return
	}

	w.updateProgress(downloaded, total, percentage, speed, eta)
}

func (w *Worker) updateProgress(downloaded, total int64, percentage float64, speed int64, eta time.Duration) {
	w.download.setProgress(downloaded, total)

	w.progressMu.Lock()
	w.lastProgress = progress.Progress{
		TotalSize:  total,
		Downloaded: downloaded,
		Percentage: percentage,
		SpeedBPS:   speed,
		ETA:        eta,
	}
	w.progressMu.Unlock()
}

func (w *Worker) setCancel(cancel context.CancelFunc) {
	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()

	w.cancel = cancel
}

func (w *Worker) getCancel() context.CancelFunc {
	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()

	return w.cancel
}

func (w *Worker) stop(s status.Status, remove bool) error {
	currentStatus := w.download.getStatus()
	if currentStatus == status.Completed || currentStatus == status.Cancelled || currentStatus == status.Failed {
		if remove {
			w.cleanupFiles()
		}

		return nil
	}

	if s == status.Paused && currentStatus == status.Paused {
		return nil
	}

	w.download.setStatus(s)

	if cancel := w.getCancel(); cancel != nil {
		cancel()
	}

	if remove {
		w.cleanupFiles()

		return nil
	}

	if w.repo != nil {
		if err := w.repo.Save(w.download); err != nil {
			return fmt.Errorf("failed to save download state: %w", err)
		}
	}

	return nil
}

func (w *Worker) cleanupFiles() {
	w.skipSave.Store(true)

	path := w.download.getPath()
	if path != "" {
		_ = os.Remove(path)
		_ = os.Remove(path + ".part")
		_ = os.Remove(path + ".ytdl")
	}

	if w.repo != nil {
		_ = w.repo.Delete(w.download.GetID().String())
	}
}

func (w *Worker) finish(err error) {
	if !w.finished.CompareAndSwap(false, true) {
		return
	}

	defer func() {
		if cancel := w.getCancel(); cancel != nil {
			cancel()
		}

		if w.repo != nil && !w.skipSave.Load() {
			if saveErr := w.repo.Save(w.download); saveErr != nil {
				logger.Errorf("failed to save download: %v", saveErr)
			}
		}

		w.started.Store(false)

		select {
		case w.done <- err:
		default:
		}
	}()

	if err != nil {
		if !errors.Is(err, context.Canceled) {
			w.download.setStatus(status.Failed)
		}

		return
	}

	current := w.download.getStatus()
	if current == status.Paused || current == status.Cancelled {
		return
	}

	w.download.setStatus(status.Completed)

	downloaded := w.download.getDownloaded()
	total := w.download.getTotalSize()
	if total == 0 {
		total = downloaded
	}

	w.progressMu.Lock()
	w.lastProgress = progress.Progress{
		TotalSize:  total,
		Downloaded: downloaded,
		Percentage: 100,
		SpeedBPS:   0,
		ETA:        0,
	}
	w.progressMu.Unlock()
}

func (w *Worker) saveState(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.repo != nil && !w.skipSave.Load() {
				if err := w.repo.Save(w.download); err != nil {
					logger.Errorf("failed to persist yt-dlp download: %v", err)
				}
			}
		}
	}
}

func parseProgress(content string) (percentage float64, total int64, downloaded int64, speed int64, eta time.Duration, ok bool) {
	fields := strings.Fields(content)
	if len(fields) == 0 {
		return 0, 0, 0, 0, 0, false
	}

	value := fields[0]
	if !strings.HasSuffix(value, "%") {
		return 0, 0, 0, 0, 0, false
	}

	pct, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}

	percentage = math.Max(0, math.Min(100, pct))

	for i := 1; i < len(fields); i++ {
		switch fields[i] {
		case "of":
			if i+1 < len(fields) {
				total = parseSize(fields[i+1])
				i++
			}
		case "at":
			if i+1 < len(fields) {
				speed = parseSpeed(fields[i+1])
				i++
			}
		case "ETA":
			if i+1 < len(fields) {
				eta = parseETA(fields[i+1])
				i++
			}
		}
	}

	if total > 0 {
		downloaded = int64(percentage / 100 * float64(total))
	}

	return percentage, total, downloaded, speed, eta, true
}

func parseSize(value string) int64 {
	cleaned := strings.Trim(value, "~,")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || strings.EqualFold(cleaned, "unknown") || strings.EqualFold(cleaned, "n/a") {
		return 0
	}

	var numPart, unitPart string
	for i, r := range cleaned {
		if (r < '0' || r > '9') && r != '.' {
			numPart = cleaned[:i]
			unitPart = cleaned[i:]

			break
		}
	}

	if numPart == "" {
		numPart = cleaned
	}

	unitPart = strings.TrimSpace(unitPart)
	num, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0
	}

	unit := strings.ToUpper(unitPart)
	switch unit {
	case "", "B":
		return int64(num)
	case "KIB":
		return int64(num * 1024)
	case "MIB":
		return int64(num * 1024 * 1024)
	case "GIB":
		return int64(num * 1024 * 1024 * 1024)
	case "TIB":
		return int64(num * 1024 * 1024 * 1024 * 1024)
	case "KB":
		return int64(num * 1000)
	case "MB":
		return int64(num * 1000 * 1000)
	case "GB":
		return int64(num * 1000 * 1000 * 1000)
	case "TB":
		return int64(num * 1000 * 1000 * 1000 * 1000)
	}

	return 0
}

func parseSpeed(value string) int64 {
	cleaned := strings.TrimSuffix(value, "/s")

	return parseSize(cleaned)
}

func parseETA(value string) time.Duration {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" || strings.EqualFold(cleaned, "unknown") || strings.EqualFold(cleaned, "n/a") {
		return 0
	}

	parts := strings.Split(cleaned, ":")
	if len(parts) == 2 {
		m, err1 := strconv.Atoi(parts[0])
		s, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0
		}

		return time.Duration(m)*time.Minute + time.Duration(s)*time.Second
	}

	if len(parts) == 3 {
		h, err1 := strconv.Atoi(parts[0])
		m, err2 := strconv.Atoi(parts[1])
		s, err3 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return 0
		}

		return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
	}

	return 0
}

func calculatePercentage(downloaded, total int64) float64 {
	if total <= 0 {
		if downloaded > 0 {
			return 100
		}

		return 0
	}

	pct := float64(downloaded) / float64(total) * 100
	if pct > 100 {
		return 100
	}

	if pct < 0 {
		return 0
	}

	return pct
}
