package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/crypto/ssh"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/remote"
	runreport "github.com/quyenhl16/script/internal/report"
	"github.com/quyenhl16/script/internal/runner"
	"github.com/quyenhl16/script/internal/workflow"
)

type tabID int

const (
	tabFeatures tabID = iota
	tabPlan
	tabLogs
	tabSSH
	tabWorkflows
	tabProfiles
)

type sshMode int

const (
	sshCommand sshMode = iota
	sshScript
	sshWorkflow
)

const (
	sshHosts = iota
	sshUser
	sshPassword
	sshCommandValue
	sshScriptPath
	sshScriptArgs
	sshWorkflowConfig
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

type workflowFinishedMsg struct {
	execution workflow.Execution
	err       error
}

type model struct {
	ctx               context.Context
	registry          *registry.Registry
	workflowRegistry  *workflow.Registry
	profile           domain.Profile
	options           runner.Options
	features          []domain.Feature
	featureIndexes    map[string]int
	selected          map[string]bool
	profileParameters map[string]map[string]any
	statuses          map[string]domain.Status
	resolved          []domain.ResolvedFeature
	workflows         []workflow.Definition
	profiles          []domain.Profile

	table           table.Model
	workflowTable   table.Model
	profileTable    table.Model
	filter          textinput.Model
	featureDetail   viewport.Model
	logs            viewport.Model
	sshOutput       viewport.Model
	spinner         spinner.Model
	activeTab       tabID
	sshInputs       []textinput.Model
	sshFocus        int
	sshMode         sshMode
	profileServers  []remote.Server
	serverSource    string
	reportFormat    runreport.Format
	reportsDir      string
	runStartedAt    time.Time
	remoteStartedAt time.Time
	remoteOperation string
	remoteKind      string
	activeWorkflow  workflow.Definition

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
	return newModelWithProfiles(ctx, registry, nil, []domain.Profile{profile}, profile, options)
}

func newModelWithWorkflows(ctx context.Context, registry *registry.Registry, workflowRegistry *workflow.Registry, profile domain.Profile, options runner.Options) *model {
	return newModelWithProfiles(ctx, registry, workflowRegistry, []domain.Profile{profile}, profile, options)
}

func newModelWithProfiles(ctx context.Context, registry *registry.Registry, workflowRegistry *workflow.Registry, profiles []domain.Profile, profile domain.Profile, options runner.Options) *model {
	reportFormat, reportFormatErr := runreport.ParseFormat(options.ReportFormat)
	if reportFormatErr != nil {
		reportFormat = runreport.Markdown
	}
	reportsDir := options.ReportsDir
	if reportsDir == "" {
		reportsDir = "reports"
	}
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
		workflowRegistry:  workflowRegistry,
		profile:           profile,
		options:           options,
		features:          registry.List(),
		featureIndexes:    make(map[string]int),
		selected:          make(map[string]bool),
		profileParameters: make(map[string]map[string]any),
		statuses:          make(map[string]domain.Status),
		profiles:          profiles,
		reportFormat:      reportFormat,
		reportsDir:        reportsDir,
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
		filter:        filter,
		featureDetail: viewport.New(0, 0),
		logs:          viewport.New(0, 0),
		sshOutput:     viewport.New(0, 0),
		spinner:       spin,
		notice:        "Ready",
		sshFocus:      -1,
	}
	m.workflowTable = table.New(
		table.WithFocused(true),
		table.WithStyles(styles),
		table.WithWidth(78),
		table.WithHeight(10),
		table.WithColumns([]table.Column{
			{Title: "#", Width: 4},
			{Title: "WORKFLOW", Width: 38},
			{Title: "VERSION", Width: 9},
			{Title: "MODE", Width: 8},
			{Title: "STEPS", Width: 7},
		}),
	)
	m.profileTable = table.New(
		table.WithFocused(true),
		table.WithStyles(styles),
		table.WithWidth(78),
		table.WithHeight(10),
		table.WithColumns([]table.Column{
			{Title: "#", Width: 4},
			{Title: "SYSTEM", Width: 16},
			{Title: "PROFILE", Width: 28},
			{Title: "FEATURES", Width: 10},
		}),
	)
	if workflowRegistry != nil {
		m.workflows = workflowRegistry.List()
	}
	m.refreshWorkflowRows()
	m.refreshProfileRows()
	m.sshInputs = newSSHInputs()
	m.sshOutput.SetContent("SSH results will appear here.")
	for index, feature := range m.features {
		m.featureIndexes[feature.ID] = index + 1
	}
	for _, item := range profile.Features {
		m.selected[item.ID] = true
		m.profileParameters[item.ID] = item.Parameters
	}
	for index, available := range m.profiles {
		if sameProfile(available, profile) {
			m.profileTable.SetCursor(index)
			break
		}
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
		finished := time.Now()
		reportPath, reportErr := runreport.Write(
			runreport.FeatureRun(m.profile, msg.results, msg.output, msg.err, m.runStartedAt, finished),
			runreport.Options{Directory: m.reportsDir, Format: m.reportFormat},
		)
		m.runErr = runreport.JoinRunAndReportErrors(m.runErr, reportErr)
		if msg.err != nil {
			m.notice = "Run failed: " + msg.err.Error()
		} else {
			m.notice = "Run completed"
		}
		m.notice = reportNotice(m.notice, reportPath, reportErr)
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
		finished := time.Now()
		reportPath, reportErr := runreport.Write(
			runreport.RemoteRun(m.profile, m.remoteOperation, m.remoteKind, msg.results, m.remoteStartedAt, finished),
			runreport.Options{Directory: m.reportsDir, Format: m.reportFormat},
		)
		m.remoteErr = runreport.JoinRunAndReportErrors(m.remoteErr, reportErr)
		m.notice = reportNotice(m.notice, reportPath, reportErr)
		return m, nil
	case workflowFinishedMsg:
		m.remoteRunning = false
		m.cancelRemote = nil
		m.activeOutput = nil
		m.remoteErr = msg.err
		var output strings.Builder
		for _, result := range msg.execution.Results {
			label := strings.ToUpper(string(result.Status))
			invocation := ""
			if result.Invocation > 0 {
				invocation = fmt.Sprintf(".%d", result.Invocation)
			}
			fmt.Fprintf(&output, "[%s] %s  %s%s  %s", label, result.Server, result.StepID, invocation, result.FeatureID)
			if result.Duration > 0 {
				fmt.Fprintf(&output, " (%s)", result.Duration.Round(time.Millisecond))
			}
			output.WriteByte('\n')
			if result.Output != "" {
				output.WriteString(result.Output)
				if !strings.HasSuffix(result.Output, "\n") {
					output.WriteByte('\n')
				}
			}
			if result.Err != nil {
				fmt.Fprintf(&output, "error: %v\n", result.Err)
				m.remoteErr = errors.Join(m.remoteErr, fmt.Errorf("%s/%s: %w", result.Server, result.StepID, result.Err))
			}
			output.WriteByte('\n')
		}
		succeeded := 0
		for _, server := range msg.execution.Servers {
			if server.Success {
				succeeded++
			}
		}
		m.sshOutput.SetContent(strings.TrimRight(output.String(), "\n"))
		m.sshOutput.GotoBottom()
		if msg.err != nil {
			m.notice = "Workflow failed: " + msg.err.Error()
		} else if m.activeWorkflow.ExecutionMode == "local" && succeeded == 0 {
			m.notice = "Local workflow completed with failures"
		} else if m.activeWorkflow.ExecutionMode == "local" {
			m.notice = "Local workflow completed successfully"
		} else {
			m.notice = fmt.Sprintf("Workflow completed: %d/%d server(s) succeeded", succeeded, len(msg.execution.Servers))
		}
		finished := time.Now()
		reportPath, reportErr := runreport.Write(
			runreport.WorkflowRun(m.profile, m.activeWorkflow, msg.execution, msg.err, m.remoteStartedAt, finished),
			runreport.Options{Directory: m.reportsDir, Format: m.reportFormat},
		)
		m.remoteErr = runreport.JoinRunAndReportErrors(m.remoteErr, reportErr)
		m.notice = reportNotice(m.notice, reportPath, reportErr)
		return m, nil
	case logTickMsg:
		if m.running && m.activeOutput != nil {
			m.logs.SetContent(m.activeOutput.String())
			m.logs.GotoBottom()
		} else if m.remoteRunning && m.activeWorkflow.ExecutionMode == "local" && m.activeOutput != nil {
			m.sshOutput.SetContent(m.activeOutput.String())
			m.sshOutput.GotoBottom()
		}
		if m.running || (m.remoteRunning && m.activeWorkflow.ExecutionMode == "local") {
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
		m.activeTab = (m.activeTab + 1) % 6
		return m, nil
	case "shift+tab":
		m.activeTab = (m.activeTab + 5) % 6
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
	case "5":
		m.activeTab = tabWorkflows
		return m, nil
	case "6":
		m.activeTab = tabProfiles
		return m, nil
	case "/":
		if m.activeTab == tabFeatures && !m.running {
			return m, m.filter.Focus()
		}
	case " ", "enter":
		if m.activeTab == tabFeatures && !m.running {
			m.toggleCurrent()
		} else if m.activeTab == tabWorkflows && !m.running && !m.remoteRunning {
			return m, m.loadCurrentWorkflow()
		} else if m.activeTab == tabProfiles && !m.running && !m.remoteRunning {
			m.loadCurrentProfile()
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
	case "f3":
		if m.activeTab == tabSSH && !m.remoteRunning {
			m.loadBaseServers()
		}
		return m, nil
	case "f4":
		m.toggleReportFormat()
		return m, nil
	case "f5":
		if m.activeTab == tabSSH && !m.remoteRunning {
			return m, m.startRemoteRun()
		}
		return m, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		if m.activeTab == tabFeatures {
			var cmd tea.Cmd
			m.featureDetail, cmd = m.featureDetail.Update(msg)
			return m, cmd
		}
	}

	var cmd tea.Cmd
	if m.activeTab == tabFeatures {
		previousID := m.currentFeatureID()
		m.table, cmd = m.table.Update(msg)
		if previousID != m.currentFeatureID() {
			m.refreshFeatureDetail(true)
		}
	} else if m.activeTab == tabLogs {
		m.logs, cmd = m.logs.Update(msg)
	} else if m.activeTab == tabSSH {
		m.sshOutput, cmd = m.sshOutput.Update(msg)
	} else if m.activeTab == tabWorkflows {
		m.workflowTable, cmd = m.workflowTable.Update(msg)
	} else if m.activeTab == tabProfiles {
		m.profileTable, cmd = m.profileTable.Update(msg)
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
		{"Config   ", "workflow-configs/prepare-setup-deploy.json", 1024},
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
	case "f3":
		if !m.remoteRunning {
			if m.sshMode == sshWorkflow && m.activeWorkflow.ExecutionMode == "local" {
				m.notice = "Local workflow does not require SSH servers"
			} else {
				m.loadBaseServers()
			}
		}
		return m, nil
	case "f4":
		m.toggleReportFormat()
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
	switch m.sshMode {
	case sshScript:
		return []int{sshHosts, sshUser, sshPassword, sshScriptPath, sshScriptArgs}
	case sshWorkflow:
		if m.activeWorkflow.ExecutionMode == "local" {
			return []int{sshWorkflowConfig}
		}
		return []int{sshHosts, sshUser, sshPassword, sshWorkflowConfig}
	default:
		return []int{sshHosts, sshUser, sshPassword, sshCommandValue}
	}
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
	m.sshMode = (m.sshMode + 1) % 3
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
	if m.sshMode == sshWorkflow {
		return m.startWorkflowRun()
	}
	servers, err := m.sshServers()
	if err != nil {
		m.remoteErr = err
		m.notice = "SSH: " + err.Error()
		return nil
	}
	knownHostsPath, err := remote.DefaultKnownHostsPath()
	var hostKeys ssh.HostKeyCallback
	if err == nil {
		hostKeys, err = remote.NewTOFUHostKeyCallback(knownHostsPath)
	}
	if err != nil {
		m.remoteErr = err
		m.notice = "SSH host keys: " + err.Error()
		return nil
	}

	request := remote.Request{Servers: servers, Timeout: remote.DefaultTimeout, HostKeys: hostKeys}
	if m.sshMode == sshScript {
		request.Script, err = remote.LoadScript(m.sshInputs[sshScriptPath].Value())
		request.ScriptArgs = strings.Fields(m.sshInputs[sshScriptArgs].Value())
	} else if m.sshMode == sshCommand {
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
	m.remoteErr = nil
	m.remoteRunning = true
	m.remoteStartedAt = time.Now()
	m.remoteOperation = "ssh-command"
	m.remoteKind = "SSH command"
	if m.sshMode == sshScript {
		name := strings.TrimSuffix(filepath.Base(m.sshInputs[sshScriptPath].Value()), filepath.Ext(m.sshInputs[sshScriptPath].Value()))
		m.remoteOperation = "ssh-script-" + name
		m.remoteKind = "SSH script"
	}
	m.notice = fmt.Sprintf("Running SSH on %d server(s)...", len(request.Servers))
	m.sshOutput.SetContent("Connecting...")
	runContext, cancel := context.WithCancel(m.ctx)
	m.cancelRemote = cancel
	return tea.Batch(m.spinner.Tick, runRemoteCmd(runContext, request))
}

func (m *model) startWorkflowRun() tea.Cmd {
	if m.workflowRegistry == nil {
		m.remoteErr = errors.New("workflow registry is not available")
		m.notice = "Workflow registry is not available"
		return nil
	}
	config, err := workflow.LoadConfig(m.sshInputs[sshWorkflowConfig].Value())
	if err != nil {
		m.remoteErr = err
		m.notice = "Workflow: " + err.Error()
		return nil
	}
	definition, found := m.workflowRegistry.Get(config.Workflow)
	if !found {
		m.remoteErr = fmt.Errorf("unknown workflow %q", config.Workflow)
		m.notice = "Workflow: " + m.remoteErr.Error()
		return nil
	}
	if err := m.workflowRegistry.ValidateConfig(definition, config); err != nil {
		m.remoteErr = err
		m.notice = "Workflow: " + err.Error()
		return nil
	}

	var servers []remote.Server
	var hostKeys ssh.HostKeyCallback
	if definition.ExecutionMode != "local" {
		servers, err = m.sshServers()
		if err != nil {
			m.remoteErr = err
			m.notice = "SSH: " + err.Error()
			return nil
		}
		knownHostsPath, hostKeyErr := remote.DefaultKnownHostsPath()
		if hostKeyErr == nil {
			hostKeys, hostKeyErr = remote.NewTOFUHostKeyCallback(knownHostsPath)
		}
		if hostKeyErr != nil {
			m.remoteErr = hostKeyErr
			m.notice = "SSH host keys: " + hostKeyErr.Error()
			return nil
		}
	}

	m.remoteErr = nil
	m.remoteRunning = true
	m.remoteStartedAt = time.Now()
	m.remoteOperation = definition.ID
	m.remoteKind = "workflow"
	m.activeWorkflow = definition
	if definition.ExecutionMode == "local" {
		m.notice = fmt.Sprintf("Running workflow %s locally...", definition.ID)
	} else {
		m.notice = fmt.Sprintf("Running workflow %s on %d server(s)...", definition.ID, len(servers))
	}
	m.sshOutput.SetContent("Starting workflow...")
	runContext, cancel := context.WithCancel(m.ctx)
	m.cancelRemote = cancel
	var liveOutput *safeBuffer
	commands := []tea.Cmd{m.spinner.Tick}
	if definition.ExecutionMode == "local" {
		liveOutput = &safeBuffer{}
		m.activeOutput = liveOutput
		commands = append(commands, logTickCmd())
	}
	commands = append(commands, runWorkflowCmd(runContext, m.workflowRegistry, definition, config, servers, hostKeys, liveOutput))
	return tea.Batch(commands...)
}

func runRemoteCmd(ctx context.Context, request remote.Request) tea.Cmd {
	return func() tea.Msg {
		return remoteFinishedMsg{results: remote.Execute(ctx, request)}
	}
}

func runWorkflowCmd(ctx context.Context, workflows *workflow.Registry, definition workflow.Definition, config workflow.Config, servers []remote.Server, hostKeys ssh.HostKeyCallback, liveOutput *safeBuffer) tea.Cmd {
	return func() tea.Msg {
		execution, err := workflows.Execute(ctx, definition, config, servers, hostKeys, liveOutput)
		return workflowFinishedMsg{execution: execution, err: err}
	}
}

func (m *model) loadCurrentWorkflow() tea.Cmd {
	row := m.workflowTable.SelectedRow()
	if len(row) < 2 {
		m.notice = "No workflow available"
		return nil
	}
	id := row[1]
	definition, found := m.workflowRegistry.Get(id)
	if !found {
		m.notice = "Unknown workflow: " + id
		return nil
	}
	m.sshMode = sshWorkflow
	m.activeWorkflow = definition
	m.sshInputs[sshWorkflowConfig].SetValue(filepath.Join("workflow-configs", id+".json"))
	m.activeTab = tabSSH
	m.notice = fmt.Sprintf("Workflow %s loaded; review config and press F5", id)
	return m.focusSSH(sshWorkflowConfig)
}

func (m *model) loadCurrentProfile() {
	index := m.profileTable.Cursor()
	if index < 0 || index >= len(m.profiles) {
		m.notice = "No profile available"
		return
	}
	profile := m.profiles[index]
	m.profile = profile
	clear(m.selected)
	clear(m.profileParameters)
	clear(m.statuses)
	m.filter.Blur()
	m.filter.SetValue("")
	m.runErr = nil
	m.profileServers = nil
	m.serverSource = ""
	m.notice = fmt.Sprintf("Profile %s loaded: %d feature(s) selected", profile.Name, len(profile.Features))
	for _, item := range profile.Features {
		m.selected[item.ID] = true
		m.profileParameters[item.ID] = item.Parameters
	}
	m.refreshPlan()
	m.refreshRows()
	m.activeTab = tabFeatures
}

func (m *model) loadBaseServers() {
	profile, found := m.baseServerProfile()
	if !found {
		m.remoteErr = errors.New("base-server profile is not available for the current system")
		m.notice = "SSH: " + m.remoteErr.Error()
		return
	}
	servers, err := remoteServersFromProfile(profile)
	if err != nil {
		m.remoteErr = err
		m.notice = "SSH: " + err.Error()
		return
	}
	m.profileServers = servers
	m.serverSource = profileLabel(profile)
	m.sshInputs[sshHosts].SetValue("")
	m.sshInputs[sshUser].SetValue("")
	m.sshInputs[sshPassword].SetValue("")
	m.remoteErr = nil
	m.notice = fmt.Sprintf("Loaded %d remote server(s) from %s", len(servers), m.serverSource)
}

func (m *model) baseServerProfile() (domain.Profile, bool) {
	if strings.EqualFold(m.profile.Name, "base-server") && len(m.profile.RemoteServers) > 0 {
		return m.profile, true
	}
	for _, profile := range m.profiles {
		if strings.EqualFold(profile.System, m.profile.System) && strings.EqualFold(profile.Name, "base-server") && len(profile.RemoteServers) > 0 {
			return profile, true
		}
	}
	return domain.Profile{}, false
}

func (m *model) sshServers() ([]remote.Server, error) {
	hosts := strings.TrimSpace(m.sshInputs[sshHosts].Value())
	if hosts == "" {
		if len(m.profileServers) == 0 {
			return nil, errors.New("enter server addresses or press F3 to load base-server")
		}
		return append([]remote.Server(nil), m.profileServers...), nil
	}
	addresses, err := remote.ParseAddresses(hosts)
	if err != nil {
		return nil, err
	}
	user := strings.TrimSpace(m.sshInputs[sshUser].Value())
	password := m.sshInputs[sshPassword].Value()
	if user == "" || password == "" {
		return nil, errors.New("user and password are required for manually entered servers")
	}
	servers := make([]remote.Server, 0, len(addresses))
	for _, address := range addresses {
		servers = append(servers, remote.Server{Address: address, User: user, Password: password})
	}
	return servers, nil
}

func remoteServersFromProfile(profile domain.Profile) ([]remote.Server, error) {
	servers := make([]remote.Server, 0, len(profile.RemoteServers))
	for index, configured := range profile.RemoteServers {
		addresses, err := remote.ParseAddresses(configured.IP)
		if err != nil || len(addresses) != 1 {
			if err == nil {
				err = errors.New("ip must contain exactly one address")
			}
			return nil, fmt.Errorf("%s remoteServers[%d]: %w", profileLabel(profile), index, err)
		}
		address := addresses[0]
		if configured.Port > 0 {
			host, _, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, fmt.Errorf("%s remoteServers[%d]: %w", profileLabel(profile), index, splitErr)
			}
			address = net.JoinHostPort(host, strconv.Itoa(configured.Port))
		}
		servers = append(servers, remote.Server{Address: address, User: configured.Username, Password: configured.Password})
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("%s has no remoteServers", profileLabel(profile))
	}
	return servers, nil
}

func sameProfile(left, right domain.Profile) bool {
	if left.Path != "" && right.Path != "" {
		return strings.EqualFold(filepath.Clean(left.Path), filepath.Clean(right.Path))
	}
	return strings.EqualFold(left.System, right.System) && strings.EqualFold(left.Name, right.Name)
}

func profileLabel(profile domain.Profile) string {
	if profile.System == "" {
		return profile.Name
	}
	return profile.System + "/" + profile.Name
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
	profile := domain.Profile{APIVersion: "syssetup/v1", Name: m.profile.Name, System: m.profile.System, Path: m.profile.Path}
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

func (m *model) toggleReportFormat() {
	if m.running || m.remoteRunning {
		m.notice = "Wait for the current run before changing report format"
		return
	}
	if m.reportFormat == runreport.HTML {
		m.reportFormat = runreport.Markdown
	} else {
		m.reportFormat = runreport.HTML
	}
	m.options.ReportFormat = string(m.reportFormat)
	m.notice = "Report format: " + strings.ToUpper(string(m.reportFormat))
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
	m.runStartedAt = time.Now()
	m.notice = fmt.Sprintf("Running %d feature(s)...", len(m.resolved))
	m.activeTab = tabLogs
	m.activeOutput = &safeBuffer{}
	runContext, cancel := context.WithCancel(m.ctx)
	m.cancelRun = cancel
	options := m.options
	options.Output = m.activeOutput
	return tea.Batch(m.spinner.Tick, logTickCmd(), runFeaturesCmd(runContext, m.resolved, options, m.activeOutput))
}

func reportNotice(base, path string, err error) string {
	if err != nil {
		return base + "; report failed: " + err.Error()
	}
	return base + "; report: " + path
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
	previousID := m.currentFeatureID()
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
	m.refreshFeatureDetail(previousID != m.currentFeatureID())
}

func (m *model) refreshWorkflowRows() {
	rows := make([]table.Row, 0, len(m.workflows))
	for index, definition := range m.workflows {
		rows = append(rows, table.Row{
			fmt.Sprintf("%d", index+1),
			definition.ID,
			definition.Version,
			workflowExecutionMode(definition),
			fmt.Sprintf("%d", len(definition.Steps)),
		})
	}
	m.workflowTable.SetRows(rows)
}

func (m *model) refreshProfileRows() {
	rows := make([]table.Row, 0, len(m.profiles))
	for index, profile := range m.profiles {
		rows = append(rows, table.Row{
			fmt.Sprintf("%d", index+1),
			profile.System,
			profile.Name,
			fmt.Sprintf("%d", len(profile.Features)),
		})
	}
	m.profileTable.SetRows(rows)
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
	m.workflowTable.SetWidth(max(leftWidth-2, 20))
	m.workflowTable.SetHeight(max(contentHeight-2, 3))
	m.workflowTable.SetColumns([]table.Column{
		{Title: "#", Width: 4},
		{Title: "WORKFLOW", Width: max(leftWidth-36, 18)},
		{Title: "VERSION", Width: 9},
		{Title: "MODE", Width: 8},
		{Title: "STEPS", Width: 7},
	})
	m.profileTable.SetWidth(max(leftWidth-2, 20))
	m.profileTable.SetHeight(max(contentHeight-2, 3))
	m.profileTable.SetColumns([]table.Column{
		{Title: "#", Width: 4},
		{Title: "SYSTEM", Width: max(min(leftWidth/4, 20), 10)},
		{Title: "PROFILE", Width: max(leftWidth-max(min(leftWidth/4, 20), 10)-18, 12)},
		{Title: "FEATURES", Width: 10},
	})
	rightWidth := m.width - leftWidth - 1
	m.featureDetail.Width = max(rightWidth-4, 10)
	m.featureDetail.Height = max(contentHeight-2, 3)
	m.refreshFeatureDetail(false)
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
