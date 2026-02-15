package tui

import "github.com/charmbracelet/lipgloss"

// Waggle brand colors
var (
	colorGold     = lipgloss.Color("#F5A623")
	colorAmber    = lipgloss.Color("#E8912D")
	colorHoney    = lipgloss.Color("#FFD700")
	colorDark     = lipgloss.Color("#1A1A2E")
	colorDimGray  = lipgloss.Color("#555555")
	colorGreen    = lipgloss.Color("#50C878")
	colorRed      = lipgloss.Color("#FF6B6B")
	colorBlue     = lipgloss.Color("#5B9BD5")
	colorCyan     = lipgloss.Color("#88C0D0")
	colorWhite    = lipgloss.Color("#E6E6E6")
	colorSubtle   = lipgloss.Color("#888888")
)

var (
	// Panel borders
	queenBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorGold).
		Padding(0, 1)

	taskBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAmber).
		Padding(0, 1)

	workerBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(0, 1)

	statusBar = lipgloss.NewStyle().
		Foreground(colorHoney).
		Bold(true).
		Padding(0, 1)

	// Text styles
	titleStyle = lipgloss.NewStyle().
		Foreground(colorGold).
		Bold(true)

	subtleStyle = lipgloss.NewStyle().
		Foreground(colorSubtle)

	queenTextStyle = lipgloss.NewStyle().
		Foreground(colorWhite)

	toolCallStyle = lipgloss.NewStyle().
		Foreground(colorCyan)

	toolResultStyle = lipgloss.NewStyle().
		Foreground(colorDimGray)

	errorStyle = lipgloss.NewStyle().
		Foreground(colorRed)

	successStyle = lipgloss.NewStyle().
		Foreground(colorGreen)

	// Task status styles
	statusStyles = map[string]lipgloss.Style{
		"pending":  lipgloss.NewStyle().Foreground(colorSubtle),
		"running":  lipgloss.NewStyle().Foreground(colorBlue).Bold(true),
		"complete": lipgloss.NewStyle().Foreground(colorGreen),
		"failed":   lipgloss.NewStyle().Foreground(colorRed),
		"retrying": lipgloss.NewStyle().Foreground(colorAmber),
	}

	statusIcons = map[string]string{
		"pending":   "⏳",
		"assigned":  "⏳",
		"running":   "🔄",
		"complete":  "✅",
		"failed":    "❌",
		"retrying":  "🔁",
		"cancelled": "⛔",
	}
)

func statusIcon(status string) string {
	if icon, ok := statusIcons[status]; ok {
		return icon
	}
	return "❓"
}

func statusStyle(status string) lipgloss.Style {
	if style, ok := statusStyles[status]; ok {
		return style
	}
	return subtleStyle
}
