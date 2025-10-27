package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NamanBalaji/tdm/internal/engine"
	"github.com/NamanBalaji/tdm/internal/tui/components"
	"github.com/NamanBalaji/tdm/internal/tui/styles"
	"github.com/NamanBalaji/tdm/internal/ytdlp"
)

type currentView int

var (
	ErrPriorityNAN   = errors.New("priority must be a number")
	ErrPriorityRange = errors.New("priority must be between 1 and 10")
)

const (
	viewList currentView = iota
	viewAdd
	viewSelectFormat
	viewConfirmRemove
	viewConfirmCancel
)

// Model is the main TUI application model.
type Model struct {
	actions           engineActions
	view              currentView
	addFormFocusIndex int

	list          listModel
	urlInput      textinput.Model
	priorityInput textinput.Model
	spinner       spinner.Model
	help          help.Model
	keys          keyMap

	formatOptions   []ytdlp.Format
	formatSelected  int
	formatLoading   bool
	pendingURL      string
	pendingPriority int
	formatFetchID   int
	activeFetchID   int

	width, height int
	errMsg        string
	successMsg    string
	lastRefresh   time.Time
	loaded        bool
}

type listModel struct {
	downloads []engine.DownloadInfo
	selected  int
}

type (
	clearMsg      struct{}
	tickMsg       struct{}
	downloadsMsg  []engine.DownloadInfo
	downloadError struct{ error }
	formatsMsg    struct {
		formats   []ytdlp.Format
		err       error
		requestID int
	}
)

func clearNotifications() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return clearMsg{}
	})
}

// NewModel creates a new TUI model.
func NewModel(actions engineActions) *Model {
	urlInput := textinput.New()
	urlInput.Placeholder = "Enter download URL"
	urlInput.Focus()
	urlInput.CharLimit = 1024
	urlInput.Width = 60

	priorityInput := textinput.New()
	priorityInput.Placeholder = "Priority (1-10, default 5)"
	priorityInput.CharLimit = 2
	priorityInput.Width = 30
	priorityInput.Validate = func(s string) error {
		if s == "" {
			return nil
		}

		p, err := strconv.Atoi(s)
		if err != nil {
			return ErrPriorityNAN
		}

		if p < 1 || p > 10 {
			return ErrPriorityRange
		}

		return nil
	}

	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = lipgloss.NewStyle().Foreground(styles.Pink)

	return &Model{
		actions:           actions,
		view:              viewList,
		addFormFocusIndex: 0,
		urlInput:          urlInput,
		priorityInput:     priorityInput,
		spinner:           sp,
		help:              help.New(),
		keys:              newKeyMap(),
		lastRefresh:       time.Now().Add(-10 * time.Second),
	}
}

// Init initializes the model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshDownloads(),
		m.spinner.Tick,
		tea.Every(500*time.Millisecond, func(t time.Time) tea.Msg {
			return tickMsg{}
		}),
	)
}

// Update handles incoming messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width

	case tickMsg:
		cmds = append(cmds, m.refreshDownloads())

		return m, tea.Batch(
			tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg{} }),
			tea.Batch(cmds...),
		)

	case downloadsMsg:
		m.list.downloads = msg
		if !m.loaded {
			m.loaded = true
		}

		if m.list.selected >= len(m.list.downloads) {
			m.list.selected = len(m.list.downloads) - 1
		}

		if m.list.selected < 0 {
			m.list.selected = 0
		}

		return m, nil

	case formatsMsg:
		if msg.requestID == 0 || msg.requestID != m.activeFetchID {
			return m, nil
		}

		m.activeFetchID = 0
		m.formatLoading = false
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("Failed to fetch formats: %v", msg.err)
			m.view = viewAdd

			return m, clearNotifications()
		}

		if len(msg.formats) == 0 {
			return m, m.finalizeAdd("")
		}

		m.formatOptions = prependDefaultFormat(msg.formats)
		if m.formatSelected >= len(m.formatOptions) {
			m.formatSelected = 0
		}

		return m, nil

	case downloadError:
		m.errMsg = msg.Error()
		return m, clearNotifications()

	case clearMsg:
		m.errMsg = ""
		m.successMsg = ""

		return m, nil

	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		// Global keybindings
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
	}

	switch m.view {
	case viewList:
		cmd = m.updateListView(msg)
	case viewAdd:
		cmd = m.updateAddView(msg)
	case viewSelectFormat:
		cmd = m.updateSelectFormatView(msg)
	case viewConfirmRemove:
		cmd = m.updateConfirmRemoveView(msg)
	case viewConfirmCancel:
		cmd = m.updateConfirmCancelView(msg)
	}

	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// View renders the TUI.
func (m *Model) View() string {
	if !m.loaded {
		return fmt.Sprintf("\n  %s Loading downloads... Please wait.\n\n", m.spinner.View())
	}

	header := renderHeader(m)
	footer := styles.FooterStyle.Width(m.width).Render(m.help.View(m.keys))
	notification := m.renderNotification()

	remainingHeight := m.height - lipgloss.Height(header) - lipgloss.Height(notification) - lipgloss.Height(footer)
	if remainingHeight < 0 {
		remainingHeight = 0
	}

	var mainContent string

	if remainingHeight > 0 {
		switch m.view {
		case viewList:
			mainContent = components.RenderDownloadList(m.list.downloads, m.list.selected, m.width, remainingHeight)
		case viewAdd:
			mainContent = m.renderAddView(remainingHeight)
		case viewSelectFormat:
			mainContent = m.renderSelectFormatView(remainingHeight)
		case viewConfirmRemove:
			mainContent = m.renderConfirmDialog("Are you sure you want to remove this download? (y/n)", remainingHeight)
		case viewConfirmCancel:
			mainContent = m.renderConfirmDialog("Are you sure you want to cancel this download? (y/n)", remainingHeight)
		}
	} else {
		mainContent = ""
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		notification,
		mainContent,
		footer,
	)
}

func (m *Model) renderAddView(height int) string {
	var b strings.Builder

	title := lipgloss.NewStyle().Bold(true).Foreground(styles.Pink).Render("Add New Download")
	b.WriteString(title)
	b.WriteString("\n\n" + m.urlInput.View())
	b.WriteString("\n\n" + m.priorityInput.View())

	err := m.priorityInput.Validate(m.priorityInput.Value())
	if err != nil {
		b.WriteString("\n" + styles.ErrorStyle.Render(err.Error()))
	}

	b.WriteString("\n\n(↑/↓ to switch, enter to confirm, esc to cancel)")

	dialog := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Pink).
		Padding(1, 2).
		Render(b.String())

	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *Model) renderSelectFormatView(height int) string {
	var b strings.Builder

	title := lipgloss.NewStyle().Bold(true).Foreground(styles.Pink).Render("Select Video Format")
	b.WriteString(title)

	if m.pendingURL != "" {
		urlStyle := lipgloss.NewStyle().Foreground(styles.Subtext0)
		b.WriteString("\n" + urlStyle.Render(m.pendingURL))
	}

	if m.formatLoading {
		b.WriteString("\n\n" + fmt.Sprintf("%s Fetching available formats...", m.spinner.View()))
	} else if len(m.formatOptions) == 0 {
		b.WriteString("\n\nNo formats available. The default yt-dlp behaviour will be used.")
	} else {
		b.WriteString("\n\nUse ↑/↓ to choose a format, enter to confirm, esc to cancel.\n\n")

		for i, option := range m.formatOptions {
			label := formatOptionLabel(option)
			if i == m.formatSelected {
				b.WriteString(styles.SelectedItemStyle.Render(label))
			} else {
				b.WriteString(styles.ListItemStyle.Render(label))
			}

			if i < len(m.formatOptions)-1 {
				b.WriteString("\n")
			}
		}
	}

	dialog := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Pink).
		Padding(1, 2).
		Width(m.width - 4).
		Render(b.String())

	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *Model) renderConfirmDialog(prompt string, height int) string {
	dialog := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Red).
		Padding(1, 2).
		Render(prompt)

	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *Model) renderNotification() string {
	if m.errMsg != "" {
		return styles.ErrorStyle.Width(m.width).Align(lipgloss.Center).Render(m.errMsg)
	}

	if m.successMsg != "" {
		return styles.SuccessStyle.Width(m.width).Align(lipgloss.Center).Render(m.successMsg)
	}

	return lipgloss.NewStyle().Height(1).Render("")
}

func renderHeader(m *Model) string {
	title := "TDM - Terminal Download Manager"
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(styles.Crust).
		Background(styles.Pink).
		Padding(0, 1).
		Width(m.width).
		Align(lipgloss.Center)
	header := headerStyle.Render(title)

	active := 0
	queued := 0
	paused := 0
	completed := 0
	failed := 0

	for _, d := range m.list.downloads {
		switch d.Status {
		case 1:
			active++
		case 2:
			paused++
		case 3:
			completed++
		case 4:
			failed++
		case 5:
			queued++
		}
	}

	statsText := fmt.Sprintf(
		"Total: %d | Active: %d | Queued: %d | Paused: %d | completed: %d | Failed: %d",
		len(m.list.downloads), active, queued, paused, completed, failed,
	)

	statsStyle := lipgloss.NewStyle().
		Foreground(styles.Text).
		Background(styles.Surface0).
		Padding(0, 1).
		Width(m.width).
		Align(lipgloss.Center)
	stats := statsStyle.Render(statsText)

	return lipgloss.JoinVertical(lipgloss.Top, header, stats)
}

func (m *Model) refreshDownloads() tea.Cmd {
	return func() tea.Msg {
		return downloadsMsg(m.actions.GetAll())
	}
}

func (m *Model) getSelectedDownloadID() (uuid.UUID, bool) {
	if len(m.list.downloads) > 0 && m.list.selected < len(m.list.downloads) {
		return m.list.downloads[m.list.selected].ID, true
	}

	return uuid.Nil, false
}

func (m *Model) updateListView(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.list.selected > 0 {
				m.list.selected--
			}
		case key.Matches(msg, m.keys.Down):
			if m.list.selected < len(m.list.downloads)-1 {
				m.list.selected++
			}
		case key.Matches(msg, m.keys.Add):
			m.view = viewAdd
			m.urlInput.Focus()

			return textinput.Blink
		case key.Matches(msg, m.keys.Pause):
			if id, ok := m.getSelectedDownloadID(); ok {
				go m.actions.Pause(id)
			}
		case key.Matches(msg, m.keys.Resume):
			if id, ok := m.getSelectedDownloadID(); ok {
				go m.actions.Resume(id)
			}
		case key.Matches(msg, m.keys.Cancel):
			if _, ok := m.getSelectedDownloadID(); ok {
				m.view = viewConfirmCancel
			}
		case key.Matches(msg, m.keys.Remove):
			if _, ok := m.getSelectedDownloadID(); ok {
				m.view = viewConfirmRemove
			}
		}
	}

	return nil
}

func (m *Model) updateAddView(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down):
			m.addFormFocusIndex = (m.addFormFocusIndex + 1) % 2
			if m.addFormFocusIndex == 0 {
				m.urlInput.Focus()
				m.priorityInput.Blur()
			} else {
				m.urlInput.Blur()
				m.priorityInput.Focus()
			}

		case key.Matches(msg, m.keys.Confirm):
			url := strings.TrimSpace(m.urlInput.Value())
			if url != "" {
				priority := 5 // Default priority

				if m.priorityInput.Value() != "" {
					p, _ := strconv.Atoi(m.priorityInput.Value())
					priority = p
				}

				m.pendingURL = url
				m.pendingPriority = priority

				if ytdlp.CanHandle(url) && m.actions.FetchFormats != nil {
					m.formatOptions = nil
					m.formatSelected = 0
					m.formatLoading = true
					m.formatFetchID++
					fetchID := m.formatFetchID
					m.activeFetchID = fetchID
					m.view = viewSelectFormat
					m.urlInput.Blur()
					m.priorityInput.Blur()

					return tea.Batch(m.fetchFormats(url, fetchID))
				}

				return m.finalizeAdd("")
			}

		case key.Matches(msg, m.keys.Back):
			m.view = viewList
			m.urlInput.SetValue("")
			m.priorityInput.SetValue("")
			m.addFormFocusIndex = 0
		}
	}

	var cmd tea.Cmd
	if m.addFormFocusIndex == 0 {
		m.urlInput, cmd = m.urlInput.Update(msg)
		cmds = append(cmds, cmd)
	} else {
		m.priorityInput, cmd = m.priorityInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

func (m *Model) updateConfirmRemoveView(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y", "enter":
			if id, ok := m.getSelectedDownloadID(); ok {
				go m.actions.Remove(id)
			}

			m.view = viewList

			return m.refreshDownloads()
		case "n", "N", "esc":
			m.view = viewList
			return nil
		}
	}

	return nil
}

func (m *Model) updateConfirmCancelView(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y", "enter":
			if id, ok := m.getSelectedDownloadID(); ok {
				go m.actions.Cancel(id)
			}

			m.view = viewList

			return m.refreshDownloads()
		case "n", "N", "esc":
			m.view = viewList
			return nil
		}
	}

	return nil
}

func (m *Model) updateSelectFormatView(msg tea.Msg) tea.Cmd {
	if m.formatLoading {
		if _, ok := msg.(tea.KeyMsg); ok {
			// Ignore key inputs while loading.
			return nil
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.formatSelected > 0 {
				m.formatSelected--
			}
		case key.Matches(msg, m.keys.Down):
			if m.formatSelected < len(m.formatOptions)-1 {
				m.formatSelected++
			}
		case key.Matches(msg, m.keys.Confirm):
			if m.formatLoading {
				return nil
			}

			formatID := ""
			if len(m.formatOptions) > 0 && m.formatSelected >= 0 && m.formatSelected < len(m.formatOptions) {
				formatID = m.formatOptions[m.formatSelected].ID
			}

			return m.finalizeAdd(formatID)
		case key.Matches(msg, m.keys.Back):
			m.view = viewAdd
			m.formatOptions = nil
			m.formatSelected = 0
			m.formatLoading = false
			m.activeFetchID = 0
			m.urlInput.Focus()

			return nil
		}
	}

	return nil
}

func (m *Model) fetchFormats(url string, requestID int) tea.Cmd {
	if m.actions.FetchFormats == nil {
		return nil
	}

	return func() tea.Msg {
		formats, err := m.actions.FetchFormats(url)
		return formatsMsg{formats: formats, err: err, requestID: requestID}
	}
}

func (m *Model) finalizeAdd(format string) tea.Cmd {
	url := strings.TrimSpace(m.pendingURL)
	if url == "" {
		url = strings.TrimSpace(m.urlInput.Value())
	}

	if url == "" {
		return nil
	}

	priority := m.pendingPriority
	if priority == 0 {
		if val := strings.TrimSpace(m.priorityInput.Value()); val != "" {
			if parsed, err := strconv.Atoi(val); err == nil {
				priority = parsed
			}
		}
	}

	if priority < 1 || priority > 10 {
		priority = 5
	}

	go func(u string, p int, f string) {
		m.actions.Add(u, p, f)
	}(url, priority, format)

	m.successMsg = "Added download for: " + url
	m.view = viewList
	m.urlInput.SetValue("")
	m.priorityInput.SetValue("")
	m.urlInput.Focus()
	m.addFormFocusIndex = 0
	m.pendingURL = ""
	m.pendingPriority = 0
	m.formatOptions = nil
	m.formatSelected = 0
	m.formatLoading = false
	m.activeFetchID = 0

	return clearNotifications()
}

func prependDefaultFormat(formats []ytdlp.Format) []ytdlp.Format {
	result := make([]ytdlp.Format, 0, len(formats)+1)
	result = append(result, ytdlp.Format{ID: "", FormatNote: "Best available"})
	result = append(result, formats...)

	return result
}

func formatOptionLabel(f ytdlp.Format) string {
	if strings.TrimSpace(f.ID) == "" {
		return "Best available (yt-dlp default)"
	}

	parts := []string{f.ID}
	if ext := strings.TrimSpace(f.Extension); ext != "" {
		parts = append(parts, ext)
	}

	if res := strings.TrimSpace(f.Resolution); res != "" {
		parts = append(parts, res)
	}

	if note := strings.TrimSpace(f.FormatNote); note != "" {
		parts = append(parts, note)
	}

	if size := formatBytes(f.Filesize); size != "" {
		parts = append(parts, "~"+size)
	}

	codecParts := make([]string, 0, 2)
	if vc := strings.TrimSpace(f.VideoCodec); vc != "" && vc != "none" {
		codecParts = append(codecParts, vc)
	}
	if ac := strings.TrimSpace(f.AudioCodec); ac != "" && ac != "none" {
		codecParts = append(codecParts, "audio:"+ac)
	}

	if len(codecParts) > 0 {
		parts = append(parts, strings.Join(codecParts, ", "))
	}

	if f.AverageBitrate > 0 {
		parts = append(parts, fmt.Sprintf("%.0f kbps", f.AverageBitrate))
	}

	return strings.Join(parts, " | ")
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return ""
	}

	const unit = 1024.0
	value := float64(bytes)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	idx := 0
	for value >= unit && idx < len(units)-1 {
		value /= unit
		idx++
	}

	if value >= 10 || idx == 0 {
		return fmt.Sprintf("%.0f %s", value, units[idx])
	}

	return fmt.Sprintf("%.1f %s", value, units[idx])
}
