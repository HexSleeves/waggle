package output

import (
	"bytes"
	"testing"
)

func TestPrinterActiveOnlyInPlainMode(t *testing.T) {
	t.Helper()

	modes := []struct {
		mode   Mode
		name   string
		active bool
	}{
		{ModePlain, "plain", true},
		{ModeTUI, "tui", false},
		{ModeJSON, "json", false},
		{ModeQuiet, "quiet", false},
	}

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			var buf bytes.Buffer
			p := NewPrinterWithWriter(m.mode, false, &buf)
			p.Info("hello %s", "world")
			hasOutput := buf.Len() > 0
			if hasOutput != m.active {
				t.Errorf("mode=%s: expected active=%v, got output=%v (len=%d)",
					m.name, m.active, hasOutput, buf.Len())
			}
		})
	}
}

func TestPrinterDebugRequiresVerbose(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinterWithWriter(ModePlain, false, &buf)
	p.Debug("hidden")
	if buf.Len() > 0 {
		t.Error("Debug printed without verbose")
	}

	buf.Reset()
	p2 := NewPrinterWithWriter(ModePlain, true, &buf)
	p2.Debug("shown")
	if buf.Len() == 0 {
		t.Error("Debug did not print with verbose")
	}
}

func TestPrinterTable(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinterWithWriter(ModePlain, false, &buf)
	p.Table(
		[]string{"Name", "Status"},
		[][]string{
			{"task1", "complete"},
			{"task2", "failed"},
		},
	)
	out := buf.String()
	if len(out) == 0 {
		t.Error("Table produced no output")
	}
	// Should contain both data values
	if !bytes.Contains(buf.Bytes(), []byte("task1")) {
		t.Error("Table missing task1")
	}
	if !bytes.Contains(buf.Bytes(), []byte("task2")) {
		t.Error("Table missing task2")
	}
}

func TestPrinterKeyValue(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinterWithWriter(ModePlain, false, &buf)
	p.KeyValue([][]string{
		{"Session", "abc123"},
		{"Status", "running"},
	})
	out := buf.String()
	if len(out) == 0 {
		t.Error("KeyValue produced no output")
	}
}

func TestStatusIcon(t *testing.T) {
	for _, status := range []string{"complete", "running", "pending", "failed", "cancelled", "retrying", "unknown"} {
		icon := StatusIcon(status)
		if icon == "" {
			t.Errorf("StatusIcon(%q) returned empty", status)
		}
	}
}

func TestSpinnerNilSafe(t *testing.T) {
	// In non-plain mode, Spinner returns nil — make sure Stop/Fail don't panic
	p := NewPrinterWithWriter(ModeQuiet, false, &bytes.Buffer{})
	sp := p.Spinner("test")
	sp.Stop("done") // should not panic
	sp.Fail("oops") // should not panic
}

func TestPrinterBulletList(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinterWithWriter(ModePlain, false, &buf)
	p.BulletList([]BulletItem{
		{Text: "item 1", Icon: "✔"},
		{Text: "item 2", Icon: "✖", Level: 1},
	})
	if buf.Len() == 0 {
		t.Error("BulletList produced no output")
	}
}

func TestPrinterDivider(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinterWithWriter(ModePlain, false, &buf)
	p.Divider()
	if buf.Len() == 0 {
		t.Error("Divider produced no output")
	}
}
