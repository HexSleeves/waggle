package safety

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/exedev/queen-bee/internal/config"
)

// Guard enforces safety constraints on worker operations
type Guard struct {
	cfg          config.SafetyConfig
	projectRoot  string
	resolvedPaths []string
}

func NewGuard(cfg config.SafetyConfig, projectRoot string) (*Guard, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}

	resolved := make([]string, 0, len(cfg.AllowedPaths))
	for _, p := range cfg.AllowedPaths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(absRoot, p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		resolved = append(resolved, abs)
	}
	if len(resolved) == 0 {
		resolved = []string{absRoot}
	}

	return &Guard{
		cfg:           cfg,
		projectRoot:   absRoot,
		resolvedPaths: resolved,
	}, nil
}

// CheckPath verifies a file path is within allowed boundaries
func (g *Guard) CheckPath(path string) error {
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.projectRoot, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	for _, allowed := range g.resolvedPaths {
		if strings.HasPrefix(abs, allowed) {
			return nil
		}
	}
	return fmt.Errorf("path %q outside allowed directories", path)
}

// CheckCommand verifies a command is not in the blocked list
func (g *Guard) CheckCommand(cmd string) error {
	lower := strings.ToLower(cmd)
	for _, blocked := range g.cfg.BlockedCommands {
		if strings.Contains(lower, strings.ToLower(blocked)) {
			return fmt.Errorf("command contains blocked pattern: %q", blocked)
		}
	}
	return nil
}

// CheckFileSize verifies a file doesn't exceed the maximum size
func (g *Guard) CheckFileSize(path string) error {
	if g.cfg.MaxFileSize <= 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil // File doesn't exist yet, that's OK
	}
	if info.Size() > g.cfg.MaxFileSize {
		return fmt.Errorf("file %q (%d bytes) exceeds max size (%d bytes)", path, info.Size(), g.cfg.MaxFileSize)
	}
	return nil
}

// IsReadOnly returns whether the system is in read-only mode
func (g *Guard) IsReadOnly() bool {
	return g.cfg.ReadOnlyMode
}

// ValidateTaskPaths checks all paths in a task's allowed_paths
func (g *Guard) ValidateTaskPaths(paths []string) error {
	for _, p := range paths {
		if err := g.CheckPath(p); err != nil {
			return err
		}
	}
	return nil
}

// ProjectRoot returns the resolved project root
func (g *Guard) ProjectRoot() string {
	return g.projectRoot
}
