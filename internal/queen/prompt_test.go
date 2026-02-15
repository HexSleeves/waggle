package queen

import (
	"regexp"
	"strings"
	"testing"

	"github.com/exedev/waggle/internal/adapter"
	"github.com/exedev/waggle/internal/config"
)

func TestBuildSystemPrompt_RendersConfigValues(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ProjectDir = "/tmp/test-project"
	cfg.Workers.DefaultAdapter = "claude-code"
	cfg.Workers.MaxParallel = 8
	cfg.Workers.MaxRetries = 3

	registry := adapter.NewRegistry()

	q := &Queen{
		cfg:      cfg,
		registry: registry,
	}

	prompt := q.buildSystemPrompt()

	if !strings.Contains(prompt, "claude-code") {
		t.Error("prompt should contain default adapter name")
	}
	if !strings.Contains(prompt, "/tmp/test-project") {
		t.Error("prompt should contain project directory")
	}
	if !strings.Contains(prompt, "8") {
		t.Error("prompt should contain max parallel value")
	}
	if !strings.Contains(prompt, "3") {
		t.Error("prompt should contain max retries value")
	}
	if !strings.Contains(prompt, "Queen Bee") {
		t.Error("prompt should contain Queen Bee identity")
	}
}

func TestBuildSystemPrompt_NoUnfilledTemplateVars(t *testing.T) {
	cfg := config.DefaultConfig()
	registry := adapter.NewRegistry()

	q := &Queen{
		cfg:      cfg,
		registry: registry,
	}

	prompt := q.buildSystemPrompt()

	// Check no {variable} placeholders remain (curly-braced template vars)
	re := regexp.MustCompile(`\{[a-z_]+\}`)
	if matches := re.FindAllString(prompt, -1); len(matches) > 0 {
		t.Errorf("prompt contains unfilled template variables: %v", matches)
	}
}

func TestBuildSystemPrompt_NilRegistry(t *testing.T) {
	cfg := config.DefaultConfig()

	q := &Queen{
		cfg:      cfg,
		registry: nil,
	}

	prompt := q.buildSystemPrompt()

	if !strings.Contains(prompt, "none") {
		t.Error("prompt should show 'none' when registry is nil")
	}
}
