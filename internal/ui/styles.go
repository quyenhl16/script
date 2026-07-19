package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary = lipgloss.Color("#00D7D7")
	colorAccent  = lipgloss.Color("#AF87FF")
	colorGreen   = lipgloss.Color("#5FD787")
	colorYellow  = lipgloss.Color("#FFD75F")
	colorRed     = lipgloss.Color("#FF5F5F")
	colorMuted   = lipgloss.Color("#808080")
	colorPanel   = lipgloss.Color("#3A3A3A")

	brandStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#101010")).
			Background(colorPrimary).
			Padding(0, 1)

	contextStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#101010")).
			Background(colorAccent).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 2)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPanel)

	panelTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	keyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorYellow)

	valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E4E4E4"))

	noticeStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)
)
