package task

import (
	"fmt"
	"sort"
	"strings"
)

// statusDOTColor returns a Graphviz color for the given task status.
func statusDOTColor(s Status) string {
	switch s {
	case StatusComplete:
		return "green"
	case StatusRunning, StatusAssigned:
		return "blue"
	case StatusPending:
		return "gold"
	case StatusFailed:
		return "red"
	case StatusRetrying:
		return "orange"
	case StatusCancelled:
		return "gray"
	default:
		return "black"
	}
}

// statusASCIIIcon returns an emoji icon for the given task status.
func statusASCIIIcon(s Status) string {
	switch s {
	case StatusComplete:
		return "\u2705"
	case StatusRunning, StatusAssigned:
		return "\U0001f504"
	case StatusPending:
		return "\u23f3"
	case StatusFailed:
		return "\u274c"
	case StatusRetrying:
		return "\U0001f501"
	case StatusCancelled:
		return "\u26d4"
	default:
		return "\u2753"
	}
}

// RenderDOT outputs the task graph in Graphviz DOT format.
// Nodes are labeled with the task title and colored by status.
// Edges run from dependency to dependent task.
func (g *TaskGraph) RenderDOT() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var b strings.Builder
	b.WriteString("digraph tasks {\n")
	b.WriteString("  rankdir=LR;\n")

	if len(g.tasks) == 0 {
		b.WriteString("}\n")
		return b.String()
	}

	// Sort task IDs for deterministic output.
	ids := make([]string, 0, len(g.tasks))
	for id := range g.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// Emit nodes.
	for _, id := range ids {
		t := g.tasks[id]
		label := t.Title
		if label == "" {
			label = id
		}
		color := statusDOTColor(t.Status)
		b.WriteString(fmt.Sprintf("  %q [label=%q, style=filled, fillcolor=%q];\n",
			id, label, color))
	}

	// Emit edges: dependency -> dependent.
	for _, id := range ids {
		t := g.tasks[id]
		for _, depID := range t.DependsOn {
			// Only emit edge if both nodes exist in the graph.
			if _, ok := g.tasks[depID]; ok {
				b.WriteString(fmt.Sprintf("  %q -> %q;\n", depID, id))
			}
		}
	}

	b.WriteString("}\n")
	return b.String()
}

// taskDepth computes the "depth" of each task (distance from root nodes)
// using BFS. Root nodes (no dependencies) are at depth 0.
func taskDepth(tasks map[string]*Task) map[string]int {
	depth := make(map[string]int, len(tasks))
	// Build reverse map: who depends on whom.
	// Compute in-degree based on deps within graph.
	inDeg := make(map[string]int, len(tasks))
	for id := range tasks {
		inDeg[id] = 0
	}
	for _, t := range tasks {
		for _, depID := range t.DependsOn {
			if _, ok := tasks[depID]; ok {
				inDeg[t.ID]++
			}
		}
	}

	// BFS from roots (in-degree 0).
	queue := make([]string, 0)
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
			depth[id] = 0
		}
	}
	sort.Strings(queue) // deterministic order

	// Build children map: depID -> list of tasks that depend on it.
	children := make(map[string][]string)
	for _, t := range tasks {
		for _, depID := range t.DependsOn {
			if _, ok := tasks[depID]; ok {
				children[depID] = append(children[depID], t.ID)
			}
		}
	}

	for i := 0; i < len(queue); i++ {
		id := queue[i]
		for _, childID := range children[id] {
			newDepth := depth[id] + 1
			if d, ok := depth[childID]; !ok || newDepth > d {
				depth[childID] = newDepth
			}
			inDeg[childID]--
			if inDeg[childID] == 0 {
				queue = append(queue, childID)
			}
		}
	}

	return depth
}

// RenderASCII outputs a simple ASCII representation of the task graph.
// Tasks are grouped by depth (distance from root nodes) and displayed
// with status icons and dependency arrows.
func (g *TaskGraph) RenderASCII(width int) string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.tasks) == 0 {
		return "(no tasks)"
	}

	if width <= 0 {
		width = 80
	}

	// Compute depth for each task.
	depths := taskDepth(g.tasks)

	// Group by depth.
	maxDepth := 0
	grouped := make(map[int][]string)
	for id, d := range depths {
		grouped[d] = append(grouped[d], id)
		if d > maxDepth {
			maxDepth = d
		}
	}

	var b strings.Builder

	for d := 0; d <= maxDepth; d++ {
		ids := grouped[d]
		sort.Strings(ids)

		// Print layer header.
		if d == 0 {
			b.WriteString("Layer 0 (roots):\n")
		} else {
			b.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("|", 1)))
			b.WriteString(fmt.Sprintf("  v\n"))
			b.WriteString(fmt.Sprintf("Layer %d:\n", d))
		}

		for _, id := range ids {
			t := g.tasks[id]
			icon := statusASCIIIcon(t.Status)
			title := t.Title
			if title == "" {
				title = id
			}
			// Truncate title if needed.
			maxTitle := width - 10
			if maxTitle < 10 {
				maxTitle = 10
			}
			if len(title) > maxTitle {
				title = title[:maxTitle-1] + "\u2026"
			}
			b.WriteString(fmt.Sprintf("  [%s %s]\n", icon, title))
		}
	}

	// Print dependency edges at the bottom.
	var edges []string
	sortedIDs := make([]string, 0, len(g.tasks))
	for id := range g.tasks {
		sortedIDs = append(sortedIDs, id)
	}
	sort.Strings(sortedIDs)

	for _, id := range sortedIDs {
		t := g.tasks[id]
		for _, depID := range t.DependsOn {
			if dep, ok := g.tasks[depID]; ok {
				depLabel := dep.Title
				if depLabel == "" {
					depLabel = depID
				}
				tLabel := t.Title
				if tLabel == "" {
					tLabel = id
				}
				edges = append(edges, fmt.Sprintf("  %s --> %s", depLabel, tLabel))
			}
		}
	}

	if len(edges) > 0 {
		b.WriteString("\nDependencies:\n")
		for _, e := range edges {
			b.WriteString(e + "\n")
		}
	}

	return b.String()
}
