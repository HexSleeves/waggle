package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if m.width == 0 {
		return "Initializing...\n"
	}

	w := m.width

	// Layout: Queen panel (top, ~50%), Tasks panel (middle), Status bar (bottom)
	queenH := m.height/2 - 2
	if queenH < 5 {
		queenH = 5
	}
	taskH := m.height - queenH - 4 // 4 = status bar + borders
	if taskH < 3 {
		taskH = 3
	}

	queenPanel := m.renderQueenPanel(w-4, queenH)
	taskPanel := m.renderTaskPanel(w-4, taskH)
	sbar := m.renderStatusBar(w)

	return queenPanel + "\n" + taskPanel + "\n" + sbar
}

func (m Model) renderQueenPanel(w, h int) string {
	title := titleStyle.Render("👑 Queen")
	if m.turn > 0 {
		title += subtleStyle.Render(fmt.Sprintf(" — turn %d/%d", m.turn, m.maxTurn))
	}

	// Get visible lines (accounting for scroll)
	lines := m.queenLines
	visibleH := h - 1 // -1 for title
	if visibleH < 1 {
		visibleH = 1
	}

	// Apply scroll from bottom
	end := len(lines) - m.queenScroll
	if end < 0 {
		end = 0
	}
	start := end - visibleH
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}

	var rendered []string
	for _, line := range lines[start:end] {
		text := line.text
		if len(text) > w-2 {
			text = text[:w-5] + "..."
		}
		switch line.style {
		case "think":
			rendered = append(rendered, queenTextStyle.Render(text))
		case "tool":
			rendered = append(rendered, toolCallStyle.Render(text))
		case "result":
			rendered = append(rendered, toolResultStyle.Render(text))
		case "error":
			rendered = append(rendered, errorStyle.Render(text))
		default:
			rendered = append(rendered, subtleStyle.Render(text))
		}
	}

	// Pad to fill height
	for len(rendered) < visibleH {
		rendered = append(rendered, "")
	}

	content := title + "\n" + strings.Join(rendered, "\n")
	return queenBorder.Width(w).Render(content)
}

func (m Model) renderTaskPanel(w, h int) string {
	title := titleStyle.Render("📋 Tasks")

	if len(m.tasks) == 0 {
		content := title + "\n" + subtleStyle.Render("  No tasks yet...")
		for i := 0; i < h-2; i++ {
			content += "\n"
		}
		return taskBorder.Width(w).Render(content)
	}

	// Count stats
	done, running, pending, failed := 0, 0, 0, 0
	for _, t := range m.tasks {
		switch t.Status {
		case "complete":
			done++
		case "running":
			running++
		case "failed":
			failed++
		default:
			pending++
		}
	}
	stats := subtleStyle.Render(fmt.Sprintf(" — %d/%d done", done, len(m.tasks)))
	if running > 0 {
		stats += lipgloss.NewStyle().Foreground(colorBlue).Render(fmt.Sprintf(" · %d running", running))
	}
	if failed > 0 {
		stats += errorStyle.Render(fmt.Sprintf(" · %d failed", failed))
	}

	// Header
	headerLine := title + stats

	// Task rows
	var rows []string
	maxRows := h - 2 // -2 for title and header
	if maxRows < 1 {
		maxRows = 1
	}

	// Determine column widths
	idW := 12
	statusW := 3
	workerW := 10
	titleW := w - idW - statusW - workerW - 10 // padding/separators
	if titleW < 10 {
		titleW = 10
	}

	for i, t := range m.tasks {
		if i >= maxRows {
			remaining := len(m.tasks) - maxRows
			rows = append(rows, subtleStyle.Render(fmt.Sprintf("  ... +%d more tasks", remaining)))
			break
		}

		icon := statusIcon(t.Status)
		style := statusStyle(t.Status)

		id := t.ID
		if len(id) > idW {
			id = id[:idW-1] + "…"
		}

		taskTitle := t.Title
		if len(taskTitle) > titleW {
			taskTitle = taskTitle[:titleW-1] + "…"
		}

		worker := t.WorkerID
		if worker == "" {
			worker = "—"
		}
		if len(worker) > workerW {
			worker = worker[:workerW-1] + "…"
		}

		row := fmt.Sprintf("  %s %s %s %s",
			icon,
			style.Render(fmt.Sprintf("%-*s", idW, id)),
			fmt.Sprintf("%-*s", titleW, taskTitle),
			subtleStyle.Render(worker),
		)
		rows = append(rows, row)
	}

	// Pad
	for len(rows) < maxRows {
		rows = append(rows, "")
	}

	content := headerLine + "\n" + strings.Join(rows, "\n")
	return taskBorder.Width(w).Render(content)
}

func (m Model) renderStatusBar(w int) string {
	elapsed := time.Since(m.startTime).Round(time.Second)

	// Left side: objective
	left := "🐝 "
	if m.done {
		if m.success {
			left += successStyle.Render("✓ Complete")
		} else {
			left += errorStyle.Render("✗ Failed")
		}
	} else {
		obj := m.objective
		if len(obj) > w/2 {
			obj = obj[:w/2-3] + "..."
		}
		left += obj
	}

	// Right side: stats
	workerCount := len(m.workers)
	right := fmt.Sprintf("%d workers · %s", workerCount, elapsed)

	// Fill middle with spaces
	midW := w - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if midW < 1 {
		midW = 1
	}

	bar := left + strings.Repeat(" ", midW) + subtleStyle.Render(right)
	return statusBar.Width(w).Render(bar)
}
