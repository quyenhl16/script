package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/runner"
)

type tabID int

const (
	tabFeatures tabID = iota
	tabPlan
	tabLogs
)

type runFinishedMsg struct {
	results []domain.Result
	output  string
	err     error
}

type logTickMsg struct{}

type model struct {
	ctx               context.Context
	registry          *registry.Registry
	profile           domain.Profile
	options           runner.Options
	features          []domain.Feature
	selected          map[string]bool
	profileParameters map[string]map[string]any
	statuses          map[string]domain.Status
	resolved          []domain.ResolvedFeature

	table     table.Model
	filter    textinput.Model
	logs      viewport.Model
	spinner   spinner.Model
	activeTab tabID

	width   int
	height  int
	help    bool
	running bool
	notice  string
	runErr  error

	activeOutput *safeBuffer
	cancelRun    context.CancelFunc
}

func newModel(ctx context.Context, registry *registry.Registry, profile domain.Profile, options runner.Options) *model {
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter features"
	filter.CharLimit = 80
	filter.PromptStyle = lipgloss.NewStyle().Foreground(colorYellow)
	filter.TextStyle = valueStyle

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	styles := table.DefaultStyles()
	styles.Header = styles.Header.Bold(true).Foreground(colorPrimary).BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(colorPanel)
	styles.Selected = styles.Selected.Foreground(lipgloss.Color("#101010")).Background(colorPrimary).Bold(true)
	styles.Cell = styles.Cell.Foreground(lipgloss.Color("#D0D0D0"))

	m := &model{
		ctx:               ctx,
		registry:          registry,
		profile:           profile,
		options:           options,
		features:          registry.List(),
		selected:          make(map[string]bool),
		profileParameters: make(map[string]map[string]any),
		statuses:          make(map[string]domain.Status),
		table: table.New(
			table.WithFocused(true),
			table.WithStyles(styles),
			table.WithWidth(78),
			table.WithHeight(10),
			table.WithColumns([]table.Column{
				{Title: "", Width: 3},
				{Title: "STATUS", Width: 9},
				{Title: "FEATURE", Width: 40},
				{Title: "VERSION", Width: 9},
				{Title: "AS", Width: 6},
			}),
		),
		filter:  filter,
		logs:    viewport.New(0, 0),
		spinner: spin,
		notice:  "Ready",
	}
	for _, item := range profile.Features {
		m.selected[item.ID] = true
		m.profileParameters[item.ID] = item.Parameters
	}
	m.refreshPlan()
	m.refreshRows()
	return m
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case runFinishedMsg:
		m.running = false
		m.cancelRun = nil
		m.runErr = msg.err
		m.logs.SetContent(msg.output)
		m.logs.GotoBottom()
		for _, result := range msg.results {
			m.statuses[result.FeatureID] = result.Status
		}
		if msg.err != nil {
			m.notice = "Run failed: " + msg.err.Error()
		} else {
			m.notice = "Run completed"
		}
		m.activeOutput = nil
		m.refreshRows()
		return m, nil
	case logTickMsg:
		if m.activeOutput != nil {
			m.logs.SetContent(m.activeOutput.String())
			m.logs.GotoBottom()
		}
		if m.running {
			return m, logTickCmd()
		}
		return m, nil
	case spinner.TickMsg:
		if m.running {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.activeTab == tabLogs {
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.help {
		if key == "?" || key == "esc" || key == "q" {
			m.help = false
		}
		return m, nil
	}
	if m.filter.Focused() {
		switch key {
		case "esc":
			m.filter.Blur()
			m.filter.SetValue("")
			m.refreshRows()
			return m, nil
		case "enter":
			m.filter.Blur()
			return m, nil
		case "ctrl+c":
			return m.quit()
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.refreshRows()
		return m, cmd
	}

	switch key {
	case "q", "ctrl+c":
		return m.quit()
	case "?":
		m.help = true
		return m, nil
	case "tab":
		m.activeTab = (m.activeTab + 1) % 3
		return m, nil
	case "shift+tab":
		m.activeTab = (m.activeTab + 2) % 3
		return m, nil
	case "1":
		m.activeTab = tabFeatures
		return m, nil
	case "2":
		m.activeTab = tabPlan
		return m, nil
	case "3":
		m.activeTab = tabLogs
		return m, nil
	case "/":
		if m.activeTab == tabFeatures && !m.running {
			return m, m.filter.Focus()
		}
	case " ", "enter":
		if m.activeTab == tabFeatures && !m.running {
			m.toggleCurrent()
		}
		return m, nil
	case "a":
		if m.activeTab == tabFeatures && !m.running {
			for _, feature := range m.filteredFeatures() {
				m.selected[feature.ID] = true
			}
			m.afterSelectionChange()
		}
		return m, nil
	case "n":
		if m.activeTab == tabFeatures && !m.running {
			clear(m.selected)
			m.afterSelectionChange()
		}
		return m, nil
	case "r":
		if !m.running {
			return m, m.startRun()
		}
	case "c":
		if m.running && m.cancelRun != nil {
			m.cancelRun()
			m.notice = "Cancelling current run..."
		}
		return m, nil
	}

	var cmd tea.Cmd
	if m.activeTab == tabFeatures {
		m.table, cmd = m.table.Update(msg)
	} else if m.activeTab == tabLogs {
		m.logs, cmd = m.logs.Update(msg)
	}
	return m, cmd
}

func (m *model) quit() (tea.Model, tea.Cmd) {
	if m.cancelRun != nil {
		m.cancelRun()
	}
	return m, tea.Quit
}

func (m *model) toggleCurrent() {
	row := m.table.SelectedRow()
	if len(row) < 3 {
		return
	}
	id := row[2]
	m.selected[id] = !m.selected[id]
	m.afterSelectionChange()
}

func (m *model) afterSelectionChange() {
	m.runErr = nil
	m.notice = fmt.Sprintf("%d feature(s) selected", m.selectedCount())
	m.refreshPlan()
	m.refreshRows()
}

func (m *model) refreshPlan() {
	profile := m.selectedProfile()
	if len(profile.Features) == 0 {
		m.resolved = nil
		return
	}
	resolved, err := m.registry.Resolve(profile)
	if err != nil {
		m.notice = err.Error()
		m.resolved = nil
		return
	}
	m.resolved = resolved
}

func (m *model) selectedProfile() domain.Profile {
	profile := domain.Profile{APIVersion: "syssetup/v1", Name: m.profile.Name}
	ids := make([]string, 0, len(m.selected))
	for id, selected := range m.selected {
		if selected {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		profile.Features = append(profile.Features, domain.FeatureSelection{ID: id, Parameters: m.profileParameters[id]})
	}
	return profile
}

func (m *model) startRun() tea.Cmd {
	m.refreshPlan()
	if len(m.resolved) == 0 {
		m.notice = "Select at least one feature"
		return nil
	}
	for _, item := range m.resolved {
		m.statuses[item.Feature.ID] = domain.StatusPlanned
	}
	m.refreshRows()
	m.runErr = nil
	m.running = true
	m.notice = fmt.Sprintf("Running %d feature(s)...", len(m.resolved))
	m.activeTab = tabLogs
	m.activeOutput = &safeBuffer{}
	runContext, cancel := context.WithCancel(m.ctx)
	m.cancelRun = cancel
	options := m.options
	options.Output = m.activeOutput
	return tea.Batch(m.spinner.Tick, logTickCmd(), runFeaturesCmd(runContext, m.resolved, options, m.activeOutput))
}

func runFeaturesCmd(ctx context.Context, features []domain.ResolvedFeature, options runner.Options, output *safeBuffer) tea.Cmd {
	return func() tea.Msg {
		executor, err := runner.New(options)
		if err != nil {
			return runFinishedMsg{output: output.String(), err: err}
		}
		results, runErr := executor.Execute(ctx, features)
		closeErr := executor.Close()
		return runFinishedMsg{results: results, output: output.String(), err: errors.Join(runErr, closeErr)}
	}
}

func logTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return logTickMsg{} })
}

func (m *model) refreshRows() {
	rows := make([]table.Row, 0, len(m.features))
	for _, feature := range m.filteredFeatures() {
		selected := "[ ]"
		if m.selected[feature.ID] {
			selected = "[x]"
		}
		root := "user"
		if feature.RequireRoot {
			root = "root"
		}
		rows = append(rows, table.Row{selected, m.statusLabel(feature.ID), feature.ID, feature.Version, root})
	}
	m.table.SetRows(rows)
}

func (m *model) filteredFeatures() []domain.Feature {
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if query == "" {
		return m.features
	}
	filtered := make([]domain.Feature, 0, len(m.features))
	for _, feature := range m.features {
		haystack := strings.ToLower(feature.ID + " " + feature.Name + " " + feature.Description)
		if strings.Contains(haystack, query) {
			filtered = append(filtered, feature)
		}
	}
	return filtered
}

func (m *model) statusLabel(id string) string {
	switch m.statuses[id] {
	case domain.StatusPlanned:
		return "QUEUED"
	case domain.StatusDone:
		return "DONE"
	case domain.StatusSkipped:
		return "SKIPPED"
	case domain.StatusFailed:
		return "FAILED"
	default:
		return "READY"
	}
}

func (m *model) selectedCount() int {
	count := 0
	for _, selected := range m.selected {
		if selected {
			count++
		}
	}
	return count
}

func (m *model) resize(width, height int) {
	m.width = max(width, 40)
	m.height = max(height, 12)
	contentHeight := max(m.height-6, 4)
	leftWidth := m.width - 2
	if m.width >= 100 {
		leftWidth = m.width*2/3 - 1
	}
	m.table.SetWidth(max(leftWidth-2, 20))
	m.table.SetHeight(max(contentHeight-2, 3))
	m.table.SetColumns([]table.Column{
		{Title: "", Width: 3},
		{Title: "STATUS", Width: 9},
		{Title: "FEATURE", Width: max(leftWidth-38, 14)},
		{Title: "VERSION", Width: 9},
		{Title: "AS", Width: 6},
	})
	m.logs.Width = max(m.width-4, 20)
	m.logs.Height = max(contentHeight-2, 3)
}
