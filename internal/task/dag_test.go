package task

import (
	"strings"
	"testing"
)

func newTestGraph(tasks ...*Task) *TaskGraph {
	g := NewTaskGraph(nil)
	for _, t := range tasks {
		g.Add(t)
	}
	return g
}

func TestRenderDOT_EmptyGraph(t *testing.T) {
	g := NewTaskGraph(nil)
	dot := g.RenderDOT()
	if !strings.Contains(dot, "digraph tasks") {
		t.Fatal("expected digraph header")
	}
	if !strings.Contains(dot, "rankdir=LR") {
		t.Fatal("expected rankdir=LR")
	}
	// Should have no nodes.
	if strings.Contains(dot, "[label=") {
		t.Fatal("expected no nodes in empty graph")
	}
}

func TestRenderDOT_LinearDeps(t *testing.T) {
	// A -> B -> C
	g := newTestGraph(
		&Task{ID: "a", Title: "Task A", Status: StatusPending},
		&Task{ID: "b", Title: "Task B", Status: StatusRunning, DependsOn: []string{"a"}},
		&Task{ID: "c", Title: "Task C", Status: StatusComplete, DependsOn: []string{"b"}},
	)

	dot := g.RenderDOT()

	// Verify nodes exist.
	if !strings.Contains(dot, `"a"`) {
		t.Error("missing node a")
	}
	if !strings.Contains(dot, `"b"`) {
		t.Error("missing node b")
	}
	if !strings.Contains(dot, `"c"`) {
		t.Error("missing node c")
	}

	// Verify edges.
	if !strings.Contains(dot, `"a" -> "b"`) {
		t.Error("missing edge a -> b")
	}
	if !strings.Contains(dot, `"b" -> "c"`) {
		t.Error("missing edge b -> c")
	}

	// Should NOT have c -> a edge.
	if strings.Contains(dot, `"c" -> "a"`) {
		t.Error("unexpected edge c -> a")
	}
}

func TestRenderDOT_ParallelTasks(t *testing.T) {
	// A, B independent; both are deps of C.
	g := newTestGraph(
		&Task{ID: "a", Title: "Task A", Status: StatusComplete},
		&Task{ID: "b", Title: "Task B", Status: StatusComplete},
		&Task{ID: "c", Title: "Task C", Status: StatusPending, DependsOn: []string{"a", "b"}},
	)

	dot := g.RenderDOT()

	if !strings.Contains(dot, `"a" -> "c"`) {
		t.Error("missing edge a -> c")
	}
	if !strings.Contains(dot, `"b" -> "c"`) {
		t.Error("missing edge b -> c")
	}
	// No edge between a and b.
	if strings.Contains(dot, `"a" -> "b"`) || strings.Contains(dot, `"b" -> "a"`) {
		t.Error("unexpected edge between a and b")
	}
}

func TestRenderDOT_StatusColors(t *testing.T) {
	g := newTestGraph(
		&Task{ID: "done", Title: "Done", Status: StatusComplete},
		&Task{ID: "run", Title: "Running", Status: StatusRunning},
		&Task{ID: "wait", Title: "Waiting", Status: StatusPending},
		&Task{ID: "err", Title: "Error", Status: StatusFailed},
	)

	dot := g.RenderDOT()

	tests := []struct {
		id    string
		color string
	}{
		{"done", "green"},
		{"run", "blue"},
		{"wait", "gold"},
		{"err", "red"},
	}

	for _, tc := range tests {
		// Find the line for this node and check its color.
		lines := strings.Split(dot, "\n")
		found := false
		for _, line := range lines {
			if strings.Contains(line, `"`+tc.id+`"`) && strings.Contains(line, "fillcolor") {
				found = true
				if !strings.Contains(line, tc.color) {
					t.Errorf("node %q: expected color %q, got line: %s", tc.id, tc.color, line)
				}
			}
		}
		if !found {
			t.Errorf("node %q not found in DOT output", tc.id)
		}
	}
}

func TestRenderASCII_EmptyGraph(t *testing.T) {
	g := NewTaskGraph(nil)
	ascii := g.RenderASCII(80)
	if ascii != "(no tasks)" {
		t.Errorf("expected '(no tasks)', got %q", ascii)
	}
}

func TestRenderASCII_LinearDeps(t *testing.T) {
	// A -> B -> C
	g := newTestGraph(
		&Task{ID: "a", Title: "Task A", Status: StatusPending},
		&Task{ID: "b", Title: "Task B", Status: StatusRunning, DependsOn: []string{"a"}},
		&Task{ID: "c", Title: "Task C", Status: StatusComplete, DependsOn: []string{"b"}},
	)

	ascii := g.RenderASCII(80)

	// Verify all tasks appear.
	if !strings.Contains(ascii, "Task A") {
		t.Error("missing Task A")
	}
	if !strings.Contains(ascii, "Task B") {
		t.Error("missing Task B")
	}
	if !strings.Contains(ascii, "Task C") {
		t.Error("missing Task C")
	}

	// Verify layers exist.
	if !strings.Contains(ascii, "Layer 0") {
		t.Error("missing Layer 0")
	}
	if !strings.Contains(ascii, "Layer 1") {
		t.Error("missing Layer 1")
	}
	if !strings.Contains(ascii, "Layer 2") {
		t.Error("missing Layer 2")
	}

	// Verify dependencies section.
	if !strings.Contains(ascii, "Task A --> Task B") {
		t.Error("missing dependency Task A --> Task B")
	}
	if !strings.Contains(ascii, "Task B --> Task C") {
		t.Error("missing dependency Task B --> Task C")
	}
}

func TestRenderASCII_ParallelTasks(t *testing.T) {
	// A, B independent; both deps of C.
	g := newTestGraph(
		&Task{ID: "a", Title: "Task A", Status: StatusComplete},
		&Task{ID: "b", Title: "Task B", Status: StatusComplete},
		&Task{ID: "c", Title: "Task C", Status: StatusPending, DependsOn: []string{"a", "b"}},
	)

	ascii := g.RenderASCII(80)

	// A and B should be in Layer 0, C in Layer 1.
	if !strings.Contains(ascii, "Layer 0") {
		t.Error("missing Layer 0")
	}
	if !strings.Contains(ascii, "Layer 1") {
		t.Error("missing Layer 1")
	}

	// Both A and B should be at the same level.
	lines := strings.Split(ascii, "\n")
	layer0Start := -1
	layer1Start := -1
	for i, line := range lines {
		if strings.Contains(line, "Layer 0") {
			layer0Start = i
		}
		if strings.Contains(line, "Layer 1") {
			layer1Start = i
		}
	}

	if layer0Start < 0 || layer1Start < 0 {
		t.Fatal("could not find both layers")
	}

	// A and B should appear between layer0Start and layer1Start.
	layer0Content := strings.Join(lines[layer0Start:layer1Start], "\n")
	if !strings.Contains(layer0Content, "Task A") {
		t.Error("Task A not in Layer 0")
	}
	if !strings.Contains(layer0Content, "Task B") {
		t.Error("Task B not in Layer 0")
	}

	// C should appear after layer1Start.
	layer1Content := strings.Join(lines[layer1Start:], "\n")
	if !strings.Contains(layer1Content, "Task C") {
		t.Error("Task C not in Layer 1")
	}

	// Dependencies.
	if !strings.Contains(ascii, "Task A --> Task C") {
		t.Error("missing dependency Task A --> Task C")
	}
	if !strings.Contains(ascii, "Task B --> Task C") {
		t.Error("missing dependency Task B --> Task C")
	}
}
