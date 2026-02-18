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

	w, h := m.normalizedSize()
	taskH := m.taskPanelHeight(h)
	innerW, queenH := m.mainPanelSize()

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

	// Render input
	inputW := w - 16
	if inputW < 30 {
		inputW = 30
	}
	if inputW > 100 {
		inputW = 100
	}
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorGold).
		Padding(0, 1).
		Width(inputW).
		Render(m.objectiveInput.View())

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
	_ = h
	title := titleStyle.Render("👑 Queen")
	if m.turn > 0 {
		title += subtleStyle.Render(fmt.Sprintf("  turn %d/%d", m.turn, m.maxTurn))
	}

	if hiddenBelow := m.queenViewport.TotalLineCount() - (m.queenViewport.YOffset + m.queenViewport.VisibleLineCount()); hiddenBelow > 0 {
		title += subtleStyle.Render(fmt.Sprintf("  [↑%d more]", hiddenBelow))
	}

	content := title + "\n" + m.queenViewport.View()
	return queenBorder.Width(w).Render(content)
}

func (m Model) renderWorkerOutputPanel(w, h int) string {
	_ = h
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

	if hiddenBelow := m.workerViewport.TotalLineCount() - (m.workerViewport.YOffset + m.workerViewport.VisibleLineCount()); hiddenBelow > 0 {
		title += subtleStyle.Render(fmt.Sprintf("  [↑%d more]", hiddenBelow))
	}

	content := title + "\n" + m.workerViewport.View()
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

	content := title + stats + "\n" + m.taskTable.View()
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
		left = "🐝 " + m.spinner.View() + " " + obj
	} else {
		left = "🐝 Waggle"
	}

	// Centre: progress bar (only when tasks exist and not done)
	var centre string
	done, _, _, total := m.taskStats()
	if total > 0 && !m.done {
		pctF := float64(done) / float64(total)
		pct := int(pctF*100 + 0.5)
		m.progress.Width = 12
		bar := m.progress.ViewAs(pctF)
		centre = fmt.Sprintf("[%d/%d %s %d%%]", done, total, bar, pct)
		eta := m.estimateETA()
		if eta > 0 {
			centre += "  " + formatETA(eta)
		}
	}

	m.help.Width = w - 2
	helpStr := m.help.View(m.keys)

	// Right: workers + time
	workerCount := len(m.workers)
	right := fmt.Sprintf("%d workers · %s", workerCount, elapsed)
	if helpStr != "" {
		right += "  " + helpStr
	}

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
	_ = h
	title := titleStyle.Render("🔀 Task DAG")
	title += subtleStyle.Render("  d:toggle")

	if hiddenBelow := m.dagViewport.TotalLineCount() - (m.dagViewport.YOffset + m.dagViewport.VisibleLineCount()); hiddenBelow > 0 {
		title += subtleStyle.Render(fmt.Sprintf("  [↓%d more]", hiddenBelow))
	}

	content := title + "\n" + m.dagViewport.View()
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

func (m Model) renderQueenViewportContent(width int) string {
	var rendered []string
	for _, line := range m.queenLines {
		wrapped := wrapText(line.text, width)
		for _, wrappedLine := range wrapped {
			switch line.style {
			case "think":
				rendered = append(rendered, queenTextStyle.Render(wrappedLine))
			case "tool":
				rendered = append(rendered, toolCallStyle.Render(wrappedLine))
			case "result":
				rendered = append(rendered, toolResultStyle.Render(wrappedLine))
			case "error":
				rendered = append(rendered, errorStyle.Render(wrappedLine))
			default:
				rendered = append(rendered, subtleStyle.Render(wrappedLine))
			}
		}
	}
	return strings.Join(rendered, "\n")
}

func (m Model) renderWorkerViewportContent(width int, workerID string) string {
	lines := m.workerOutputs[workerID]
	var rendered []string
	for _, line := range lines {
		wrapped := wrapText(line, width)
		for _, wrappedLine := range wrapped {
			rendered = append(rendered, workerOutputStyle.Render(wrappedLine))
		}
	}
	return strings.Join(rendered, "\n")
}

func (m Model) renderDAGViewportContent(width int) string {
	if len(m.tasks) == 0 {
		return subtleStyle.Render("  No tasks yet...")
	}

	ascii := m.buildTaskDAGASCII(width)
	lines := strings.Split(ascii, "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, queenTextStyle.Render(line))
	}
	return strings.Join(rendered, "\n")
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
