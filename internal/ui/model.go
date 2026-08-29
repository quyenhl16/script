package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	"github.com/quyenhl16/script/internal/remote"
	"github.com/quyenhl16/script/internal/runner"
)

type tabID int

const (
	tabFeatures tabID = iota
	tabPlan
	tabLogs
	tabSSH
)

type sshMode int

const (
	sshCommand sshMode = iota
	sshScript
)

const (
	sshHosts = iota
	sshUser
	sshPassword
	sshCommandValue
	sshScriptPath
	sshScriptArgs
)

type runFinishedMsg struct {
	results []domain.Result
	output  string
	err     error
}

type logTickMsg struct{}

type remoteFinishedMsg struct {
	results []remote.Result
}

type model struct {
	ctx               context.Context
	registry          *registry.Registry
	profile           domain.Profile
	options           runner.Options
	features          []domain.Feature
	featureIndexes    map[string]int
	selected          map[string]bool
	profileParameters map[string]map[string]any
	statuses          map[string]domain.Status
	resolved          []domain.ResolvedFeature

	table     table.Model
	filter    textinput.Model
	logs      viewport.Model
	sshOutput viewport.Model
	spinner   spinner.Model
	activeTab tabID
	sshInputs []textinput.Model
	sshFocus  int
	sshMode   sshMode

	width         int
	height        int
	help          bool
	running       bool
	remoteRunning bool
	notice        string
	runErr        error
	remoteErr     error

	activeOutput *safeBuffer
	cancelRun    context.CancelFunc
	cancelRemote context.CancelFunc
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
		featureIndexes:    make(map[string]int),
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
				{Title: "#", Width: 4},
				{Title: "STATUS", Width: 9},
				{Title: "FEATURE", Width: 35},
				{Title: "VERSION", Width: 9},
				{Title: "AS", Width: 6},
			}),
		),
		filter:    filter,
		logs:      viewport.New(0, 0),
		sshOutput: viewport.New(0, 0),
		spinner:   spin,
		notice:    "Ready",
		sshFocus:  -1,
	}
	m.sshInputs = newSSHInputs()
	m.sshOutput.SetContent("SSH results will appear here.")
	for index, feature := range m.features {
		m.featureIndexes[feature.ID] = index + 1
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
	case remoteFinishedMsg:
		m.remoteRunning = false
		m.cancelRemote = nil
		m.remoteErr = nil
		succeeded := 0
		var output strings.Builder
		for _, result := range msg.results {
			status := "PASS"
			if result.Err != nil {
				status = "FAIL"
				m.remoteErr = errors.Join(m.remoteErr, fmt.Errorf("%s: %w", result.Address, result.Err))
			} else {
				succeeded++
			}
			fmt.Fprintf(&output, "[%s] %s (%s)\n", status, result.Address, result.Duration.Round(time.Millisecond))
			if result.Output != "" {
				output.WriteString(result.Output)
				if !strings.HasSuffix(result.Output, "\n") {
					output.WriteByte('\n')
				}
			}
			if result.Err != nil {
				fmt.Fprintf(&output, "error: %v\n", result.Err)
			}
			output.WriteByte('\n')
		}
		m.sshOutput.SetContent(strings.TrimRight(output.String(), "\n"))
		m.sshOutput.GotoBottom()
		m.notice = fmt.Sprintf("SSH completed: %d/%d server(s) succeeded", succeeded, len(msg.results))
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
		if m.running || m.remoteRunning {
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
	if m.activeTab == tabSSH && m.sshFocus >= 0 {
		return m.handleSSHInput(msg)
	}

	switch key {
	case "q", "ctrl+c":
		return m.quit()
	case "?":
		m.help = true
		return m, nil
	case "tab":
		m.activeTab = (m.activeTab + 1) % 4
		return m, nil
	case "shift+tab":
		m.activeTab = (m.activeTab + 3) % 4
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
	case "4":
		m.activeTab = tabSSH
		return m, m.focusSSH(sshHosts)
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
		if m.activeTab == tabSSH && !m.remoteRunning {
			return m, m.startRemoteRun()
		}
		if !m.running && !m.remoteRunning {
			return m, m.startRun()
		}
	case "c":
		if m.running && m.cancelRun != nil {
			m.cancelRun()
			m.notice = "Cancelling current run..."
		}
		return m, nil
	case "f2":
		if m.activeTab == tabSSH && !m.remoteRunning {
			m.toggleSSHMode()
		}
		return m, nil
	case "f5":
		if m.activeTab == tabSSH && !m.remoteRunning {
			return m, m.startRemoteRun()
		}
		return m, nil
	}

	var cmd tea.Cmd
	if m.activeTab == tabFeatures {
		m.table, cmd = m.table.Update(msg)
	} else if m.activeTab == tabLogs {
		m.logs, cmd = m.logs.Update(msg)
	} else if m.activeTab == tabSSH {
		m.sshOutput, cmd = m.sshOutput.Update(msg)
	}
	return m, cmd
}

func (m *model) quit() (tea.Model, tea.Cmd) {
	if m.cancelRun != nil {
		m.cancelRun()
	}
	if m.cancelRemote != nil {
		m.cancelRemote()
	}
	return m, tea.Quit
}

func newSSHInputs() []textinput.Model {
	definitions := []struct {
		prompt      string
		placeholder string
		limit       int
	}{
		{"Servers  ", "10.0.0.10, server.example:2222", 512},
		{"User     ", "root", 128},
		{"Password ", "password", 256},
		{"Command  ", "uname -a", 2048},
		{"Script   ", "./scripts/remote/create_bond_vlan.sh", 1024},
		{"Args     ", "bond2.306 ip=10.0.36.87 prefix=24 gateway=10.0.36.254", 2048},
	}
	inputs := make([]textinput.Model, len(definitions))
	for index, definition := range definitions {
		input := textinput.New()
		input.Prompt = definition.prompt
		input.Placeholder = definition.placeholder
		input.CharLimit = definition.limit
		input.PromptStyle = keyStyle
		input.TextStyle = valueStyle
		if index == sshPassword {
			input.EchoMode = textinput.EchoPassword
			input.EchoCharacter = '•'
		}
		inputs[index] = input
	}
	inputs[sshScriptPath].SetValue("features/create-bond-vlan/run.sh")
	return inputs
}

func (m *model) handleSSHInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.sshInputs[m.sshFocus].Blur()
		m.sshFocus = -1
		return m, nil
	case "tab", "down", "enter":
		m.moveSSHFocus(1)
		return m, nil
	case "shift+tab", "up":
		m.moveSSHFocus(-1)
		return m, nil
	case "f2":
		if !m.remoteRunning {
			m.toggleSSHMode()
		}
		return m, nil
	case "f5":
		if !m.remoteRunning {
			return m, m.startRemoteRun()
		}
		return m, nil
	case "ctrl+c":
		if m.remoteRunning && m.cancelRemote != nil {
			m.cancelRemote()
			m.notice = "Cancelling SSH execution..."
			return m, nil
		}
		return m.quit()
	}
	if m.remoteRunning {
		return m, nil
	}
	var cmd tea.Cmd
	m.sshInputs[m.sshFocus], cmd = m.sshInputs[m.sshFocus].Update(msg)
	return m, cmd
}

func (m *model) sshFieldOrder() []int {
	if m.sshMode == sshScript {
		return []int{sshHosts, sshUser, sshPassword, sshScriptPath, sshScriptArgs}
	}
	return []int{sshHosts, sshUser, sshPassword, sshCommandValue}
}

func (m *model) focusSSH(field int) tea.Cmd {
	for index := range m.sshInputs {
		m.sshInputs[index].Blur()
	}
	m.sshFocus = field
	return m.sshInputs[field].Focus()
}

func (m *model) moveSSHFocus(delta int) {
	order := m.sshFieldOrder()
	position := 0
	for index, field := range order {
		if field == m.sshFocus {
			position = index
			break
		}
	}
	position = (position + delta + len(order)) % len(order)
	m.focusSSH(order[position])
}

func (m *model) toggleSSHMode() {
	if m.sshMode == sshCommand {
		m.sshMode = sshScript
	} else {
		m.sshMode = sshCommand
	}
	order := m.sshFieldOrder()
	valid := false
	for _, field := range order {
		valid = valid || field == m.sshFocus
	}
	if m.sshFocus >= 0 && !valid {
		m.focusSSH(order[len(order)-1])
	}
}

func (m *model) startRemoteRun() tea.Cmd {
	if m.running {
		m.remoteErr = errors.New("a feature plan is already running")
		m.notice = "Wait for the feature plan to finish before starting SSH"
		return nil
	}
	addresses, err := remote.ParseAddresses(m.sshInputs[sshHosts].Value())
	if err != nil {
		m.remoteErr = err
		m.notice = "SSH: " + err.Error()
		return nil
	}
	user := strings.TrimSpace(m.sshInputs[sshUser].Value())
	password := m.sshInputs[sshPassword].Value()
	if user == "" || password == "" {
		m.remoteErr = errors.New("user and password are required")
		m.notice = "SSH: user and password are required"
		return nil
	}
	request := remote.Request{Timeout: remote.DefaultTimeout}
	for _, address := range addresses {
		request.Servers = append(request.Servers, remote.Server{Address: address, User: user, Password: password})
	}
	if m.sshMode == sshScript {
		request.Script, err = remote.LoadScript(m.sshInputs[sshScriptPath].Value())
		request.ScriptArgs = strings.Fields(m.sshInputs[sshScriptArgs].Value())
	} else {
		request.Command = strings.TrimSpace(m.sshInputs[sshCommandValue].Value())
		if request.Command == "" {
			err = errors.New("enter a command")
		}
	}
	if err != nil {
		m.remoteErr = err
		m.notice = "SSH: " + err.Error()
		return nil
	}
	knownHostsPath, err := remote.DefaultKnownHostsPath()
	if err == nil {
		request.HostKeys, err = remote.NewTOFUHostKeyCallback(knownHostsPath)
	}
	if err != nil {
		m.remoteErr = err
		m.notice = "SSH host keys: " + err.Error()
		return nil
	}
	m.remoteErr = nil
	m.remoteRunning = true
	m.notice = fmt.Sprintf("Running SSH on %d server(s)...", len(request.Servers))
	m.sshOutput.SetContent("Connecting...")
	runContext, cancel := context.WithCancel(m.ctx)
	m.cancelRemote = cancel
	return tea.Batch(m.spinner.Tick, runRemoteCmd(runContext, request))
}

func runRemoteCmd(ctx context.Context, request remote.Request) tea.Cmd {
	return func() tea.Msg {
		return remoteFinishedMsg{results: remote.Execute(ctx, request)}
	}
}

func (m *model) toggleCurrent() {
	row := m.table.SelectedRow()
	if len(row) < 4 {
		return
	}
	id := row[3]
	for _, feature := range m.features {
		if feature.ID == id && feature.RemoteOnly {
			m.sshMode = sshScript
			m.sshInputs[sshScriptPath].SetValue(filepath.Join(feature.Directory, feature.Entrypoint))
			m.sshInputs[sshScriptArgs].SetValue("")
			m.sshInputs[sshScriptArgs].Placeholder = feature.RemoteArgsExample
			m.activeTab = tabSSH
			m.notice = fmt.Sprintf("Remote feature %s loaded; enter arguments and press F5", feature.ID)
			m.focusSSH(sshScriptArgs)
			return
		}
	}
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
		if feature.RemoteOnly {
			root = "remote"
		}
		index := fmt.Sprintf("%d", m.featureIndexes[feature.ID])
		rows = append(rows, table.Row{selected, index, m.statusLabel(feature.ID), feature.ID, feature.Version, root})
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
		{Title: "#", Width: 4},
		{Title: "STATUS", Width: 9},
		{Title: "FEATURE", Width: max(leftWidth-43, 14)},
		{Title: "VERSION", Width: 9},
		{Title: "AS", Width: 6},
	})
	m.logs.Width = max(m.width-4, 20)
	m.logs.Height = max(contentHeight-2, 3)
	m.sshOutput.Width = max(m.width/2-6, 20)
	m.sshOutput.Height = max(contentHeight-5, 3)
	inputWidth := max(m.width/2-16, 20)
	if m.width < 100 {
		inputWidth = max(m.width-16, 20)
		m.sshOutput.Width = max(m.width-6, 20)
		m.sshOutput.Height = max(contentHeight-17, 1)
	}
	for index := range m.sshInputs {
		m.sshInputs[index].Width = inputWidth
	}
}
