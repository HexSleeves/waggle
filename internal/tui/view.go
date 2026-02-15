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

	w := m.width
	if w < 40 {
		w = 80 // sensible default before WindowSizeMsg
	}
	h := m.height
	if h < 10 {
		h = 24
	}

	// Dynamic layout: give task panel only what it needs, queen gets the rest
	taskRows := len(m.tasks)
	if taskRows == 0 {
		taskRows = 1
	}
	taskH := taskRows + 3 // rows + title + border padding
	if taskH > h/3 {
		taskH = h / 3 // cap at 1/3 of screen
	}

	statusH := 1
	queenH := h - taskH - statusH - 4 // borders eat ~4 lines
	if queenH < 5 {
		queenH = 5
	}

	innerW := w - 2 // border eats 2 chars

	queenPanel := m.renderQueenPanel(innerW, queenH)
	taskPanel := m.renderTaskPanel(innerW, taskH)
	sbar := m.renderStatusBar(w)

	return queenPanel + "\n" + taskPanel + "\n" + sbar
}

func (m Model) renderQueenPanel(w, h int) string {
	// Title with scroll indicator
	title := titleStyle.Render("👑 Queen")
	if m.turn > 0 {
		title += subtleStyle.Render(fmt.Sprintf("  turn %d/%d", m.turn, m.maxTurn))
	}

	totalLines := len(m.queenLines)
	visibleH := h - 1 // title takes 1 line
	if visibleH < 1 {
		visibleH = 1
	}

	// Scroll indicator
	if m.queenScroll > 0 {
		title += subtleStyle.Render(fmt.Sprintf("  [↑%d more]", m.queenScroll))
	}
	if totalLines > visibleH+m.queenScroll {
		above := totalLines - visibleH - m.queenScroll
		title += subtleStyle.Render(fmt.Sprintf("  [↑%d above]", above))
	}

	// Compute visible window
	end := totalLines - m.queenScroll
	if end > totalLines {
		end = totalLines
	}
	if end < 0 {
		end = 0
	}
	start := end - visibleH
	if start < 0 {
		start = 0
	}

	var rendered []string
	for _, line := range m.queenLines[start:end] {
		// Word-wrap long lines instead of truncating
		wrapped := wrapText(line.text, w-2)
		for _, wl := range wrapped {
			switch line.style {
			case "think":
				rendered = append(rendered, queenTextStyle.Render(wl))
			case "tool":
				rendered = append(rendered, toolCallStyle.Render(wl))
			case "result":
				rendered = append(rendered, toolResultStyle.Render(wl))
			case "error":
				rendered = append(rendered, errorStyle.Render(wl))
			default:
				rendered = append(rendered, subtleStyle.Render(wl))
			}
		}
	}

	// Trim to fit and pad
	if len(rendered) > visibleH {
		rendered = rendered[len(rendered)-visibleH:]
	}
	for len(rendered) < visibleH {
		rendered = append(rendered, "")
	}

	content := title + "\n" + strings.Join(rendered, "\n")
	return queenBorder.Width(w).Render(content)
}

func (m Model) renderTaskPanel(w, h int) string {
	title := titleStyle.Render("📋 Tasks")

	if len(m.tasks) == 0 {
		content := title + "\n" + subtleStyle.Render("  Waiting for Queen to create tasks...")
		return taskBorder.Width(w).Render(content)
	}

	// Stats
	done, running, failed := 0, 0, 0
	for _, t := range m.tasks {
		switch t.Status {
		case "complete":
			done++
		case "running":
			running++
		case "failed":
			failed++
		}
	}

	stats := subtleStyle.Render(fmt.Sprintf("  %d/%d", done, len(m.tasks)))
	if running > 0 {
		stats += lipgloss.NewStyle().Foreground(colorBlue).Render(fmt.Sprintf(" · %d🔄", running))
	}
	if failed > 0 {
		stats += errorStyle.Render(fmt.Sprintf(" · %d❌", failed))
	}

	// Column widths
	titleW := w - 30 // leave room for status + worker
	if titleW < 20 {
		titleW = 20
	}

	var rows []string
	maxRows := h - 2
	if maxRows < 1 {
		maxRows = 1
	}

	for i, t := range m.tasks {
		if i >= maxRows {
			remaining := len(m.tasks) - maxRows
			rows = append(rows, subtleStyle.Render(fmt.Sprintf("  +%d more", remaining)))
			break
		}

		icon := statusIcon(t.Status)
		style := statusStyle(t.Status)

		taskTitle := t.Title
		if taskTitle == "" {
			taskTitle = t.ID
		}
		if len(taskTitle) > titleW {
			taskTitle = taskTitle[:titleW-1] + "…"
		}

		worker := ""
		if t.WorkerID != "" {
			// Shorten worker ID: "worker-code-123456" -> "w-123456"
			wid := t.WorkerID
			if len(wid) > 12 {
				wid = "w-" + wid[len(wid)-6:]
			}
			worker = subtleStyle.Render(wid)
		}

		row := fmt.Sprintf("  %s %s  %s",
			icon,
			style.Render(fmt.Sprintf("%-*s", titleW, taskTitle)),
			worker,
		)
		rows = append(rows, row)
	}

	content := title + stats + "\n" + strings.Join(rows, "\n")
	return taskBorder.Width(w).Render(content)
}

func (m Model) renderStatusBar(w int) string {
	elapsed := time.Since(m.startTime).Round(time.Second)

	// Left: status + objective
	var left string
	if m.done {
		if m.success {
			left = "🐝 " + successStyle.Render("✓ Complete")
		} else {
			left = "🐝 " + errorStyle.Render("✗ Failed")
		}
		left += subtleStyle.Render("  press any key to exit")
	} else {
		obj := m.objective
		maxObj := w/2 - 5
		if maxObj > 0 && len(obj) > maxObj {
			obj = obj[:maxObj] + "…"
		}
		left = "🐝 " + obj
	}

	// Right: workers + time
	workerCount := len(m.workers)
	right := fmt.Sprintf("%d workers · %s", workerCount, elapsed)

	// Spacing
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := w - leftW - rightW - 2
	if gap < 1 {
		gap = 1
	}

	bar := left + strings.Repeat(" ", gap) + subtleStyle.Render(right)
	return statusBar.Width(w - 2).Render(bar)
}

// wrapText wraps a string to fit within maxWidth, splitting on word boundaries.
func wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		maxWidth = 80
	}
	if len(text) == 0 {
		return []string{""}
	}
	if len(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	for len(text) > maxWidth {
		// Find last space before maxWidth
		cut := maxWidth
		if idx := strings.LastIndex(text[:maxWidth], " "); idx > maxWidth/3 {
			cut = idx
		}
		lines = append(lines, text[:cut])
		text = strings.TrimLeft(text[cut:], " ")
	}
	if text != "" {
		lines = append(lines, text)
	}
	return lines
}
