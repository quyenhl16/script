package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/workflow"
)

func (m *model) View() string {
	if m.width == 0 {
		return "Starting syssetup dashboard..."
	}
	if m.help {
		return m.helpView()
	}
	header := m.headerView()
	tabs := m.tabsView()
	contentHeight := max(m.height-lipgloss.Height(header)-lipgloss.Height(tabs)-2, 4)
	var content string
	switch m.activeTab {
	case tabPlan:
		content = m.planView(contentHeight)
	case tabLogs:
		content = m.logsView(contentHeight)
	case tabSSH:
		content = m.sshView(contentHeight)
	case tabWorkflows:
		content = m.workflowsView(contentHeight)
	case tabProfiles:
		content = m.profilesView(contentHeight)
	default:
		content = m.featuresView(contentHeight)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, content, m.statusView(), m.footerView())
}

func (m *model) headerView() string {
	mode := "LIVE"
	modeStyle := errorStyle
	if m.options.DryRun {
		mode = "DRY-RUN"
		modeStyle = noticeStyle
	}
	left := brandStyle.Render("SYSSETUP") + "  " + contextStyle.Render("RHEL FEATURE DASHBOARD")
	right := fmt.Sprintf("Profile: %s  Selected: %d  Mode: %s", m.profile.Name, m.selectedCount(), modeStyle.Render(mode))
	space := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", space) + right
}

func (m *model) tabsView() string {
	tabs := []string{"1 Features", "2 Plan", "3 Logs", "4 Remote SSH", "5 Workflows", "6 Profiles"}
	parts := make([]string, len(tabs))
	for index, label := range tabs {
		if tabID(index) == m.activeTab {
			parts[index] = activeTabStyle.Render(label)
		} else {
			parts[index] = inactiveTabStyle.Render(label)
		}
	}
	filter := ""
	if m.filter.Focused() || m.filter.Value() != "" {
		filter = "  " + m.filter.View()
	}
	return strings.Join(parts, "") + filter
}

func (m *model) sshView(height int) string {
	mode := "COMMAND"
	modeHint := "Run one Linux command on every server"
	activeInputs := []int{sshCommandValue}
	switch m.sshMode {
	case sshScript:
		mode = "SCRIPT"
		modeHint = "Upload a local script through stdin and run it with bash"
		activeInputs = []int{sshScriptPath, sshScriptArgs}
	case sshWorkflow:
		mode = "WORKFLOW"
		modeHint = "Run a configured step-by-step workflow on every server"
		activeInputs = []int{sshWorkflowConfig}
	}
	formLines := []string{
		panelTitleStyle.Render("Remote SSH") + "  " + contextStyle.Render(mode),
		mutedStyle.Render(modeHint),
		"",
		m.sshInputs[sshHosts].View(),
		m.sshInputs[sshUser].View(),
		m.sshInputs[sshPassword].View(),
	}
	for _, input := range activeInputs {
		formLines = append(formLines, m.sshInputs[input].View())
	}
	formLines = append(formLines,
		"",
		mutedStyle.Render("Servers: comma/space separated host[:port]; default port is 22."),
		mutedStyle.Render("Password stays in memory and is never written to logs."),
		mutedStyle.Render("Host keys use trust-on-first-use and changed keys are rejected."),
	)
	if m.width < 100 {
		visibleFields := 7
		if m.sshMode == sshScript {
			visibleFields = 8
		}
		formLines = append(formLines[:visibleFields], "", mutedStyle.Render("Comma-separated host[:port]; F2 mode, F5 run."))
	}
	formWidth := m.width - 2
	if m.width >= 100 {
		formWidth = m.width / 2
	}
	formHeight := max(height-2, 2)
	if m.width < 100 {
		formHeight = 10
	}
	form := panelStyle.Width(max(formWidth-2, 20)).Height(formHeight).Render(strings.Join(formLines, "\n"))
	if m.width < 100 {
		title := panelTitleStyle.Render("Per-server results")
		if m.remoteRunning {
			title += "  " + m.spinner.View() + noticeStyle.Render("running")
		}
		result := panelStyle.Width(max(formWidth-2, 20)).Height(max(height-formHeight-5, 2)).Render(title + "\n" + m.sshOutput.View())
		return lipgloss.JoinVertical(lipgloss.Left, form, result)
	}
	resultWidth := m.width - formWidth - 1
	title := panelTitleStyle.Render("Per-server results")
	if m.remoteRunning {
		title += "  " + m.spinner.View() + noticeStyle.Render("running")
	}
	result := panelStyle.Width(max(resultWidth-2, 20)).Height(max(height-2, 2)).Render(title + "\n\n" + m.sshOutput.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, form, " ", result)
}

func (m *model) workflowsView(height int) string {
	leftWidth := m.width - 2
	if m.width >= 100 {
		leftWidth = m.width*2/3 - 1
	}
	left := panelStyle.Width(max(leftWidth-2, 20)).Height(max(height-2, 2)).Render(m.workflowTable.View())
	if m.width < 100 {
		return left
	}
	rightWidth := m.width - leftWidth - 1
	detail := panelStyle.Width(max(rightWidth-2, 20)).Height(max(height-2, 2)).Render(m.workflowDetailView())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", detail)
}

func (m *model) profilesView(height int) string {
	leftWidth := m.width - 2
	if m.width >= 100 {
		leftWidth = m.width*2/3 - 1
	}
	left := panelStyle.Width(max(leftWidth-2, 20)).Height(max(height-2, 2)).Render(m.profileTable.View())
	if m.width < 100 {
		return left
	}
	rightWidth := m.width - leftWidth - 1
	detail := panelStyle.Width(max(rightWidth-2, 20)).Height(max(height-2, 2)).Render(m.profileDetailView())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", detail)
}

func (m *model) profileDetailView() string {
	profile, found := m.currentProfile()
	if !found {
		return mutedStyle.Render("No profile available")
	}
	lines := []string{
		panelTitleStyle.Render(profile.Name),
		mutedStyle.Render(profile.Description),
		"",
		keyStyle.Render("Features"),
	}
	for _, item := range profile.Features {
		parameterNames := make([]string, 0, len(item.Parameters))
		for name := range item.Parameters {
			parameterNames = append(parameterNames, name)
		}
		sort.Strings(parameterNames)
		detail := ""
		if len(parameterNames) > 0 {
			detail = mutedStyle.Render("  params: " + strings.Join(parameterNames, ", "))
		}
		lines = append(lines, valueStyle.Render(item.ID)+detail)
	}
	lines = append(lines, "", mutedStyle.Render("Press Enter to load this profile."))
	return strings.Join(lines, "\n")
}

func (m *model) workflowDetailView() string {
	definition, found := m.currentWorkflow()
	if !found {
		return mutedStyle.Render("No workflow available")
	}
	lines := []string{
		panelTitleStyle.Render(definition.Name),
		mutedStyle.Render(definition.Description),
		"",
		keyStyle.Render("ID") + "       " + valueStyle.Render(definition.ID),
		keyStyle.Render("Version") + "  " + valueStyle.Render(definition.Version),
		"",
		keyStyle.Render("Steps"),
	}
	for index, step := range definition.Steps {
		dependency := ""
		if len(step.Needs) > 0 {
			dependency = mutedStyle.Render(" after " + strings.Join(step.Needs, ", "))
		}
		lines = append(lines, fmt.Sprintf("%d.%d  %s%s", m.workflowIndex(definition.ID), index+1, step.Feature, dependency))
	}
	lines = append(lines, "", mutedStyle.Render("Press Enter to load this workflow."))
	return strings.Join(lines, "\n")
}

func (m *model) featuresView(height int) string {
	leftWidth := m.width - 2
	if m.width >= 100 {
		leftWidth = m.width*2/3 - 1
	}
	left := panelStyle.Width(max(leftWidth-2, 20)).Height(max(height-2, 2)).Render(m.table.View())
	if m.width < 100 {
		return left
	}
	rightWidth := m.width - leftWidth - 1
	detail := panelStyle.Width(max(rightWidth-2, 20)).Height(max(height-2, 2)).Render(m.featureDetail.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", detail)
}

func (m *model) refreshFeatureDetail(reset bool) {
	feature, found := m.currentFeature()
	if !found {
		m.featureDetail.SetContent(mutedStyle.Render("No feature selected"))
		if reset {
			m.featureDetail.GotoTop()
		}
		return
	}
	m.featureDetail.SetContent(m.featureDetailContent(feature, max(m.featureDetail.Width, 10)))
	if reset {
		m.featureDetail.GotoTop()
	}
}

func (m *model) featureDetailContent(feature domain.Feature, width int) string {
	dependencies := "none"
	if len(feature.DependsOn) > 0 {
		dependencies = strings.Join(feature.DependsOn, ", ")
	}
	osList := "any"
	if len(feature.SupportedOS) > 0 {
		osList = strings.Join(feature.SupportedOS, ", ")
	}
	rows := []string{
		panelTitleStyle.Render(feature.Name),
		mutedStyle.Render(feature.Description),
		"",
		keyStyle.Render("ID") + "          " + valueStyle.Render(feature.ID),
		keyStyle.Render("Version") + "     " + valueStyle.Render(feature.Version),
		keyStyle.Render("Entrypoint") + "  " + valueStyle.Render(feature.Entrypoint),
		keyStyle.Render("Privilege") + "   " + valueStyle.Render(privilege(feature.RequireRoot)),
		keyStyle.Render("Execution") + "   " + valueStyle.Render(executionMode(feature.RemoteOnly)),
		keyStyle.Render("Timeout") + "     " + valueStyle.Render(fmt.Sprintf("%ds", feature.TimeoutSeconds)),
		"",
		keyStyle.Render("Depends on"),
		valueStyle.Render(dependencies),
		"",
		keyStyle.Render("Supported OS"),
		valueStyle.Render(osList),
		"",
	}
	rows = append(rows, m.parameterDetails(feature)...)
	rows = append(rows, "", mutedStyle.Render("PgUp/PgDn scroll details"))
	return lipgloss.NewStyle().Width(max(width, 10)).Render(strings.Join(rows, "\n"))
}

func (m *model) parameterDetails(feature domain.Feature) []string {
	requiredCount := 0
	for _, parameter := range feature.Parameters {
		if parameter.Required {
			requiredCount++
		}
	}
	optionalCount := len(feature.Parameters) - requiredCount
	lines := []string{
		keyStyle.Render(fmt.Sprintf("Parameters (%d required, %d optional)", requiredCount, optionalCount)),
	}
	if len(feature.Parameters) == 0 {
		return append(lines, mutedStyle.Render("none"))
	}
	lines = append(lines, mutedStyle.Render(`Configure under this feature's "parameters" object in the profile.`), "")

	names := make([]string, 0, len(feature.Parameters))
	for name := range feature.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	configured := m.profileParameters[feature.ID]
	for index, name := range names {
		parameter := feature.Parameters[name]
		requirement := mutedStyle.Render("optional")
		if parameter.Required {
			requirement = errorStyle.Render("required")
		}
		lines = append(lines, valueStyle.Render(name)+"  "+requirement+mutedStyle.Render(" - "+parameter.Type))
		if parameter.Description != "" {
			lines = append(lines, mutedStyle.Render("  "+parameter.Description))
		}
		if value, exists := configured[name]; exists {
			lines = append(lines, contextStyle.Render("  profile: ")+valueStyle.Render(formatParameterValue(name, value)))
		} else if parameter.Required {
			lines = append(lines, errorStyle.Render("  profile: MISSING - add this parameter"))
		} else if parameter.Default != nil {
			lines = append(lines, mutedStyle.Render("  default: ")+valueStyle.Render(formatParameterValue(name, parameter.Default)))
		} else {
			lines = append(lines, mutedStyle.Render("  profile: not set"))
		}
		if index < len(names)-1 {
			lines = append(lines, "")
		}
	}
	return lines
}

func formatParameterValue(name string, value any) string {
	normalized := strings.ToLower(name)
	for _, marker := range []string{"password", "secret", "token", "private_key"} {
		if strings.Contains(normalized, marker) {
			return "<configured>"
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func (m *model) planView(height int) string {
	lines := []string{panelTitleStyle.Render(fmt.Sprintf("Execution plan · %d step(s)", len(m.resolved))), ""}
	if len(m.resolved) == 0 {
		lines = append(lines, mutedStyle.Render("Select features in the Features tab to build a plan."))
	}
	for index, item := range m.resolved {
		dependency := ""
		if len(item.Feature.DependsOn) > 0 {
			dependency = mutedStyle.Render("  after: " + strings.Join(item.Feature.DependsOn, ", "))
		}
		line := fmt.Sprintf("%s  %2d  %-22s %-5s timeout %ds%s",
			contextStyle.Render("│"), index+1, item.Feature.ID, privilege(item.Feature.RequireRoot), item.Feature.TimeoutSeconds, dependency)
		lines = append(lines, line)
	}
	return panelStyle.Width(max(m.width-4, 20)).Height(max(height-2, 2)).Render(strings.Join(lines, "\n"))
}

func (m *model) logsView(height int) string {
	title := panelTitleStyle.Render("Execution logs")
	if m.running {
		title += "  " + m.spinner.View() + noticeStyle.Render("running")
	}
	content := title + "\n" + m.logs.View()
	return panelStyle.Width(max(m.width-4, 20)).Height(max(height-2, 2)).Render(content)
}

func (m *model) statusView() string {
	message := m.notice
	if m.runErr != nil || m.remoteErr != nil {
		return errorStyle.MaxWidth(m.width).Render("! " + message)
	}
	if m.running || m.remoteRunning {
		return noticeStyle.MaxWidth(m.width).Render(m.spinner.View() + " " + message)
	}
	return mutedStyle.MaxWidth(m.width).Render("• " + message)
}

func (m *model) footerView() string {
	if m.activeTab == tabSSH {
		if m.remoteRunning {
			return keyStyle.Render("ctrl+c") + " cancel  " + keyStyle.Render("esc") + " navigation"
		}
		return strings.Join([]string{
			keyStyle.Render("tab/↑↓") + " field",
			keyStyle.Render("F2") + " command/script/workflow",
			keyStyle.Render("F5") + " run",
			keyStyle.Render("esc") + " navigation",
		}, "  ")
	}
	if m.activeTab == tabWorkflows {
		return keyStyle.Render("j/k") + " move  " + keyStyle.Render("enter") + " load workflow  " + keyStyle.Render("tab") + " view  " + keyStyle.Render("q") + " quit"
	}
	if m.activeTab == tabProfiles {
		return keyStyle.Render("j/k") + " move  " + keyStyle.Render("enter") + " load profile  " + keyStyle.Render("tab") + " view  " + keyStyle.Render("q") + " quit"
	}
	items := []string{
		keyStyle.Render("j/k") + " move",
		keyStyle.Render("pgup/pgdn") + " details",
		keyStyle.Render("space") + " select",
		keyStyle.Render("/") + " filter",
		keyStyle.Render("r") + " run",
		keyStyle.Render("tab") + " view",
		keyStyle.Render("?") + " help",
		keyStyle.Render("q") + " quit",
	}
	if m.running {
		items[4] = keyStyle.Render("c") + " cancel"
	}
	return strings.Join(items, "  ")
}

func (m *model) helpView() string {
	content := strings.Join([]string{
		panelTitleStyle.Render("SYSSETUP KEYBOARD SHORTCUTS"),
		"",
		keyStyle.Render("j / k, arrows") + "   Move through features or logs",
		keyStyle.Render("space / enter") + "   Toggle selected feature",
		keyStyle.Render("a / n") + "           Select all visible / select none",
		keyStyle.Render("/") + "               Filter features",
		keyStyle.Render("PgUp / PgDn") + "     Scroll feature parameters",
		keyStyle.Render("1 / 2 / 3") + "       Features / Plan / Logs",
		keyStyle.Render("4") + "               Remote SSH form",
		keyStyle.Render("5") + "               Workflows",
		keyStyle.Render("6") + "               Profiles (Enter to load)",
		keyStyle.Render("tab / shift+tab") + " Switch dashboard view",
		keyStyle.Render("r") + "               Execute the current plan",
		keyStyle.Render("c") + "               Cancel the running plan",
		keyStyle.Render("F2 / F5") + "         SSH mode / run remote task",
		keyStyle.Render("? / esc") + "         Close this help",
		keyStyle.Render("q / ctrl+c") + "      Quit",
	}, "\n")
	box := panelStyle.Padding(1, 2).Width(min(max(m.width-12, 40), 72)).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m *model) currentFeature() (domain.Feature, bool) {
	row := m.table.SelectedRow()
	if len(row) < 4 {
		return domain.Feature{}, false
	}
	for _, feature := range m.features {
		if feature.ID == row[3] {
			return feature, true
		}
	}
	return domain.Feature{}, false
}

func (m *model) currentFeatureID() string {
	feature, found := m.currentFeature()
	if !found {
		return ""
	}
	return feature.ID
}

func (m *model) currentWorkflow() (workflow.Definition, bool) {
	row := m.workflowTable.SelectedRow()
	if len(row) < 2 || m.workflowRegistry == nil {
		return workflow.Definition{}, false
	}
	return m.workflowRegistry.Get(row[1])
}

func (m *model) currentProfile() (domain.Profile, bool) {
	index := m.profileTable.Cursor()
	if index < 0 || index >= len(m.profiles) {
		return domain.Profile{}, false
	}
	return m.profiles[index], true
}

func (m *model) workflowIndex(id string) int {
	for index, definition := range m.workflows {
		if definition.ID == id {
			return index + 1
		}
	}
	return 0
}

func privilege(root bool) string {
	if root {
		return "root"
	}
	return "user"
}

func executionMode(remoteOnly bool) string {
	if remoteOnly {
		return "Remote SSH"
	}
	return "Local runner"
}
