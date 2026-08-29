package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/quyenhl16/script/internal/domain"
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
	tabs := []string{"1 Features", "2 Plan", "3 Logs", "4 Remote SSH"}
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
	if m.sshMode == sshScript {
		mode = "SCRIPT"
		modeHint = "Upload a local script through stdin and run it with bash"
		activeInputs = []int{sshScriptPath, sshScriptArgs}
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
	detail := panelStyle.Width(max(rightWidth-2, 20)).Height(max(height-2, 2)).Render(m.detailView(rightWidth - 4))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", detail)
}

func (m *model) detailView(width int) string {
	feature, found := m.currentFeature()
	if !found {
		return mutedStyle.Render("No feature selected")
	}
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
	}
	return lipgloss.NewStyle().Width(max(width, 10)).Render(strings.Join(rows, "\n"))
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
			keyStyle.Render("F2") + " command/script",
			keyStyle.Render("F5") + " run",
			keyStyle.Render("esc") + " navigation",
		}, "  ")
	}
	items := []string{
		keyStyle.Render("j/k") + " move",
		keyStyle.Render("space") + " select",
		keyStyle.Render("/") + " filter",
		keyStyle.Render("r") + " run",
		keyStyle.Render("tab") + " view",
		keyStyle.Render("?") + " help",
		keyStyle.Render("q") + " quit",
	}
	if m.running {
		items[3] = keyStyle.Render("c") + " cancel"
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
		keyStyle.Render("1 / 2 / 3") + "       Features / Plan / Logs",
		keyStyle.Render("4") + "               Remote SSH form",
		keyStyle.Render("tab / shift+tab") + " Switch dashboard view",
		keyStyle.Render("r") + "               Execute the current plan",
		keyStyle.Render("c") + "               Cancel the running plan",
		keyStyle.Render("F2 / F5") + "         SSH mode / run SSH",
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
