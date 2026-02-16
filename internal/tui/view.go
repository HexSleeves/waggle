package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
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

	// Input mode: full-screen prompt
	if m.input == inputWaiting {
		return m.renderInputView(w, h)
	}

	var mainPanel string
	switch m.viewMode {
	case viewWorker:
		mainPanel = m.renderWorkerOutputPanel(innerW, queenH)
	case viewDAG:
		mainPanel = m.renderDAGPanel(innerW, queenH)
	default:
		mainPanel = m.renderQueenPanel(innerW, queenH)
	}

	taskPanel := m.renderTaskPanel(innerW, taskH)
	sbar := m.renderStatusBar(w)

	return mainPanel + "\n" + taskPanel + "\n" + sbar
}

func (m Model) renderInputView(w, h int) string {
	// Centered prompt
	title := lipgloss.NewStyle().
		Foreground(colorGold).
		Bold(true).
		Render("🐝 Waggle")

	subtitle := subtleStyle.Render("Multi-agent orchestration through the waggle dance.")

	promptLabel := lipgloss.NewStyle().
		Foreground(colorHoney).
		Bold(true).
		Render("Objective:")

	// Render input with cursor
	inputW := w - 16
	if inputW < 30 {
		inputW = 30
	}
	if inputW > 100 {
		inputW = 100
	}

	// Build display text with cursor
	text := m.inputText
	var displayText string
	if m.inputCursor < len(text) {
		before := text[:m.inputCursor]
		cursorChar := string(text[m.inputCursor])
		after := text[m.inputCursor+1:]
		displayText = queenTextStyle.Render(before) +
			lipgloss.NewStyle().Reverse(true).Render(cursorChar) +
			queenTextStyle.Render(after)
	} else {
		displayText = queenTextStyle.Render(text) +
			lipgloss.NewStyle().Reverse(true).Render(" ")
	}

	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorGold).
		Padding(0, 1).
		Width(inputW).
		Render(displayText)

	hint := subtleStyle.Render("Enter to start · Ctrl+C to quit")

	// Center everything vertically
	lines := []string{"", "", title, subtitle, "", promptLabel, inputBox, "", hint}
	content := strings.Join(lines, "\n")

	// Center horizontally
	centered := lipgloss.NewStyle().
		Width(w).
		Height(h).
		Align(lipgloss.Center, lipgloss.Center).
		Render(content)

	return centered
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

func (m Model) renderWorkerOutputPanel(w, h int) string {
	// Title with worker info
	workerLabel := m.viewWorkerID
	if len(workerLabel) > 20 {
		workerLabel = "w-" + workerLabel[len(workerLabel)-8:]
	}
	title := titleStyle.Render("⚙ Worker " + workerLabel)

	// Show task name if known
	if taskTitle, ok := m.workerTasks[m.viewWorkerID]; ok && taskTitle != "" {
		tt := taskTitle
		if len(tt) > w/2 {
			tt = tt[:w/2-1] + "…"
		}
		title += subtleStyle.Render("  " + tt)
	}

	// Worker index indicator
	for i, id := range m.workerOrder {
		if id == m.viewWorkerID {
			title += subtleStyle.Render(fmt.Sprintf("  [%d/%d]", i+1, len(m.workerOrder)))
			break
		}
	}

	lines := m.workerOutputs[m.viewWorkerID]
	totalLines := len(lines)
	visibleH := h - 1 // title takes 1 line
	if visibleH < 1 {
		visibleH = 1
	}

	// Scroll indicator
	if m.workerScroll > 0 {
		title += subtleStyle.Render(fmt.Sprintf("  [↑%d more]", m.workerScroll))
	}

	// Compute visible window (from bottom)
	end := totalLines - m.workerScroll
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
	for _, line := range lines[start:end] {
		wrapped := wrapText(line, w-2)
		for _, wl := range wrapped {
			rendered = append(rendered, workerOutputStyle.Render(wl))
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
	return workerBorder.Width(w).Render(content)
}

func (m Model) renderTaskPanel(w, h int) string {
	title := titleStyle.Render("📋 Tasks")

	if len(m.tasks) == 0 {
		content := title + "\n" + subtleStyle.Render("  Waiting for Queen to create tasks...")
		return taskBorder.Width(w).Render(content)
	}

	// Stats
	done, running, failed, total := m.taskStats()

	stats := subtleStyle.Render(fmt.Sprintf("  %d/%d complete", done, total))
	if running > 0 {
		stats += lipgloss.NewStyle().Foreground(colorBlue).Render(fmt.Sprintf(" · %d running", running))
	}
	if failed > 0 {
		stats += errorStyle.Render(fmt.Sprintf(" · %d failed", failed))
	}
	if eta := m.estimateETA(); eta > 0 && !m.done {
		stats += subtleStyle.Render(" · " + formatETA(eta))
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

		// Highlight if we're viewing this task's worker
		viewing := m.viewMode == viewWorker && t.WorkerID == m.viewWorkerID
		if viewing {
			row := fmt.Sprintf("▸ %s %s  %s",
				icon,
				lipgloss.NewStyle().Foreground(colorBlue).Bold(true).Render(fmt.Sprintf("%-*s", titleW, taskTitle)),
				worker,
			)
			rows = append(rows, row)
		} else {
			row := fmt.Sprintf("  %s %s  %s",
				icon,
				style.Render(fmt.Sprintf("%-*s", titleW, taskTitle)),
				worker,
			)
			rows = append(rows, row)
		}
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
	} else if m.objective != "" {
		obj := m.objective
		maxObj := w/2 - 5
		if maxObj > 0 && len(obj) > maxObj {
			obj = obj[:maxObj] + "…"
		}
		left = "🐝 " + obj
	} else {
		left = "🐝 Waggle"
	}

	// Centre: progress bar (only when tasks exist and not done)
	var centre string
	done, _, _, total := m.taskStats()
	if total > 0 && !m.done {
		pct := 0
		if total > 0 {
			pct = 100 * done / total
		}
		barW := 12
		bar := progressBar(done, total, barW)
		centre = fmt.Sprintf("[%d/%d %s %d%%]", done, total, bar, pct)
		eta := m.estimateETA()
		if eta > 0 {
			centre += "  " + formatETA(eta)
		}
	}

	// Navigation hint
	var navHint string
	if len(m.workerOrder) > 0 {
		if m.viewMode == viewWorker {
			navHint = " · tab:next  0:queen"
		} else {
			navHint = " · tab:workers"
		}
	}

	// Right: workers + time
	workerCount := len(m.workers)
	right := fmt.Sprintf("%d workers · %s%s", workerCount, elapsed, navHint)

	// Spacing: distribute between left-centre and centre-right
	leftW := lipgloss.Width(left)
	centreW := lipgloss.Width(centre)
	rightW := lipgloss.Width(right)
	totalContent := leftW + centreW + rightW
	spare := w - totalContent - 2
	if spare < 2 {
		spare = 2
	}
	var gap1, gap2 int
	if centreW > 0 {
		gap1 = spare / 2
		gap2 = spare - gap1
		if gap1 < 1 {
			gap1 = 1
		}
		if gap2 < 1 {
			gap2 = 1
		}
	} else {
		gap1 = spare
		gap2 = 0
	}

	var statusLine string
	if centreW > 0 {
		statusLine = left + strings.Repeat(" ", gap1) +
			subtleStyle.Render(centre) +
			strings.Repeat(" ", gap2) +
			subtleStyle.Render(right)
	} else {
		statusLine = left + strings.Repeat(" ", gap1) + subtleStyle.Render(right)
	}
	return statusBar.Width(w - 2).Render(statusLine)
}

func (m Model) renderDAGPanel(w, h int) string {
	title := titleStyle.Render("🔀 Task DAG")
	title += subtleStyle.Render("  d:toggle")

	visibleH := h - 1
	if visibleH < 1 {
		visibleH = 1
	}

	if len(m.tasks) == 0 {
		content := title + "\n" + subtleStyle.Render("  No tasks yet...")
		return queenBorder.Width(w).Render(content)
	}

	// Build a mini task graph from TaskInfo.
	ascii := m.buildTaskDAGASCII(w - 4)

	lines := strings.Split(ascii, "\n")
	var rendered []string
	for _, line := range lines {
		rendered = append(rendered, queenTextStyle.Render(line))
	}

	// Trim or pad to fit.
	if len(rendered) > visibleH {
		rendered = rendered[:visibleH]
	}
	for len(rendered) < visibleH {
		rendered = append(rendered, "")
	}

	content := title + "\n" + strings.Join(rendered, "\n")
	return queenBorder.Width(w).Render(content)
}

// buildTaskDAGASCII builds an ASCII DAG from the current task list.
func (m Model) buildTaskDAGASCII(width int) string {
	if len(m.tasks) == 0 {
		return "(no tasks)"
	}

	if width <= 0 {
		width = 80
	}

	// Build a lookup for computing depths.
	type miniTask struct {
		id        string
		title     string
		status    string
		dependsOn []string
	}

	tasksByID := make(map[string]*miniTask, len(m.tasks))
	for i := range m.tasks {
		t := &m.tasks[i]
		tasksByID[t.ID] = &miniTask{
			id:        t.ID,
			title:     t.Title,
			status:    t.Status,
			dependsOn: t.DependsOn,
		}
	}

	// Compute depths using BFS.
	inDeg := make(map[string]int, len(tasksByID))
	for id := range tasksByID {
		inDeg[id] = 0
	}
	children := make(map[string][]string)
	for _, t := range tasksByID {
		for _, depID := range t.dependsOn {
			if _, ok := tasksByID[depID]; ok {
				inDeg[t.id]++
				children[depID] = append(children[depID], t.id)
			}
		}
	}

	depth := make(map[string]int)
	var queue []string
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
			depth[id] = 0
		}
	}
	sort.Strings(queue)

	for i := 0; i < len(queue); i++ {
		id := queue[i]
		for _, childID := range children[id] {
			newD := depth[id] + 1
			if d, ok := depth[childID]; !ok || newD > d {
				depth[childID] = newD
			}
			inDeg[childID]--
			if inDeg[childID] == 0 {
				queue = append(queue, childID)
			}
		}
	}

	// Group by depth.
	maxDepth := 0
	grouped := make(map[int][]string)
	for id, d := range depth {
		grouped[d] = append(grouped[d], id)
		if d > maxDepth {
			maxDepth = d
		}
	}

	var b strings.Builder
	for d := 0; d <= maxDepth; d++ {
		ids := grouped[d]
		sort.Strings(ids)

		if d > 0 {
			b.WriteString("    |\n")
			b.WriteString("    v\n")
		}

		for _, id := range ids {
			t := tasksByID[id]
			icon := statusIcon(t.status)
			title := t.title
			if title == "" {
				title = id
			}
			maxTitle := width - 8
			if maxTitle < 10 {
				maxTitle = 10
			}
			if len(title) > maxTitle {
				title = title[:maxTitle-1] + "…"
			}
			b.WriteString(fmt.Sprintf("  [%s %s]\n", icon, title))
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// wrapText wraps a string to fit within maxWidth display columns,
// correctly handling emoji and CJK characters.
func wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		maxWidth = 80
	}
	if len(text) == 0 {
		return []string{""}
	}
	if runewidth.StringWidth(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	for runewidth.StringWidth(text) > maxWidth {
		// Find the byte offset that fits within maxWidth display columns
		colW := 0
		byteOff := 0
		for i, r := range text {
			rw := runewidth.RuneWidth(r)
			if colW+rw > maxWidth {
				break
			}
			colW += rw
			byteOff = i + len(string(r))
		}
		if byteOff == 0 {
			// Single character wider than maxWidth — force advance
			_, size := []rune(text)[0], len(string([]rune(text)[0]))
			byteOff = size
		}
		// Try to break on a space within the last third
		cut := byteOff
		if idx := strings.LastIndex(text[:byteOff], " "); idx > byteOff/3 {
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
