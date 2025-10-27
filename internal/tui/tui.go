package tui

import (
	"context"

	"github.com/google/uuid"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NamanBalaji/tdm/internal/engine"
	"github.com/NamanBalaji/tdm/internal/ytdlp"
)

// Run initializes and starts the TUI.
func Run(ctx context.Context, eng *engine.Engine) error {
	m := NewModel(newEngineActions(ctx, eng))
	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-eng.GetErrors():
				if !ok {
					return
				}

				p.Send(downloadError{err.Error})
			}
		}
	}()

	_, err := p.Run()

	return err
}

type engineActions struct {
	Pause        func(id uuid.UUID)
	Resume       func(id uuid.UUID)
	Add          func(url string, prio int, format string)
	Cancel       func(id uuid.UUID)
	Remove       func(id uuid.UUID)
	GetAll       func() []engine.DownloadInfo
	FetchFormats func(url string) ([]ytdlp.Format, error)
}

func newEngineActions(ctx context.Context, e *engine.Engine) engineActions {
	return engineActions{
		Pause:  func(id uuid.UUID) { e.PauseDownload(ctx, id) },
		Resume: func(id uuid.UUID) { e.ResumeDownload(ctx, id) },
		Add:    func(url string, p int, format string) { e.AddDownload(ctx, url, p, format) },
		Cancel: func(id uuid.UUID) { e.CancelDownload(ctx, id) },
		Remove: func(id uuid.UUID) { e.RemoveDownload(ctx, id) },
		GetAll: e.GetAllDownloads,
		FetchFormats: func(url string) ([]ytdlp.Format, error) {
			return e.ListFormats(ctx, url)
		},
	}
}
