package safety

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exedev/waggle/internal/config"
)

func TestNewGuard_ValidConfig(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		AllowedPaths:    []string{root},
		BlockedCommands: []string{"rm -rf /"},
		MaxFileSize:     1024,
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatalf("NewGuard() unexpected error: %v", err)
	}
	if g == nil {
		t.Fatal("NewGuard() returned nil guard")
	}
}

func TestNewGuard_RelativeAllowedPaths(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.SafetyConfig{
		AllowedPaths: []string{"subdir"},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatalf("NewGuard() unexpected error: %v", err)
	}

	// The relative path "subdir" should have been resolved to an absolute path
	for _, p := range g.resolvedPaths {
		if !filepath.IsAbs(p) {
			t.Errorf("resolved path %q is not absolute", p)
		}
	}
	if !strings.HasSuffix(g.resolvedPaths[0], "subdir") {
		t.Errorf("expected resolved path to end with 'subdir', got %q", g.resolvedPaths[0])
	}
}

func TestNewGuard_EmptyAllowedPaths(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{}

	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatalf("NewGuard() unexpected error: %v", err)
	}

	// With no allowed paths, should default to project root
	abs, _ := filepath.Abs(root)
	if len(g.resolvedPaths) != 1 || g.resolvedPaths[0] != abs {
		t.Errorf("expected resolvedPaths=[%q], got %v", abs, g.resolvedPaths)
	}
}

func TestCheckPath_Allowed(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "somefile.txt")
	if err := g.CheckPath(path); err != nil {
		t.Errorf("CheckPath(%q) = %v, want nil", path, err)
	}
}

func TestCheckPath_OutsideAllowed(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	outside := "/tmp/somewhere-else-entirely"
	if err := g.CheckPath(outside); err == nil {
		t.Error("CheckPath() for path outside allowed dirs should return error")
	}
}

func TestCheckPath_RelativeResolved(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	// A relative path should be resolved against project root
	if err := g.CheckPath("somefile.txt"); err != nil {
		t.Errorf("CheckPath(relative) = %v, want nil (should resolve under project root)", err)
	}
}

func TestCheckPath_AbsoluteCheckedDirectly(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()

	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	// Absolute path in a different temp dir should be rejected
	abs := filepath.Join(other, "file.txt")
	if err := g.CheckPath(abs); err == nil {
		t.Errorf("CheckPath(%q) = nil, want error for path outside allowed dirs", abs)
	}
}

func TestCheckCommand_NotBlocked(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		BlockedCommands: []string{"rm -rf /", "sudo rm"},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CheckCommand("ls -la"); err != nil {
		t.Errorf("CheckCommand('ls -la') = %v, want nil", err)
	}
}

func TestCheckCommand_BlockedRmRf(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		BlockedCommands: []string{"rm -rf /"},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CheckCommand("rm -rf /"); err == nil {
		t.Error("CheckCommand('rm -rf /') = nil, want error")
	}
}

func TestCheckCommand_BlockedSudoRm(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		BlockedCommands: []string{"sudo rm"},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CheckCommand("sudo rm -rf /var"); err == nil {
		t.Error("CheckCommand('sudo rm -rf /var') = nil, want error")
	}
}

func TestCheckCommand_CaseInsensitive(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		BlockedCommands: []string{"rm -rf /"},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CheckCommand("RM -RF /"); err == nil {
		t.Error("CheckCommand('RM -RF /') = nil, want error (case-insensitive match)")
	}
}

func TestCheckFileSize_SmallFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "small.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
		MaxFileSize:  1024,
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CheckFileSize(path); err != nil {
		t.Errorf("CheckFileSize(small file) = %v, want nil", err)
	}
}

func TestCheckFileSize_LargeFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	data := make([]byte, 2048)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
		MaxFileSize:  1024,
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CheckFileSize(path); err == nil {
		t.Error("CheckFileSize(large file) = nil, want error")
	}
}

func TestCheckFileSize_NonexistentFile(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
		MaxFileSize:  1024,
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "nonexistent.txt")
	if err := g.CheckFileSize(path); err != nil {
		t.Errorf("CheckFileSize(nonexistent) = %v, want nil", err)
	}
}

func TestCheckFileSize_Disabled(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	data := make([]byte, 2048)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
		MaxFileSize:  0, // disabled
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.CheckFileSize(path); err != nil {
		t.Errorf("CheckFileSize(disabled) = %v, want nil", err)
	}
}

func TestIsReadOnly_DefaultFalse(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if g.IsReadOnly() {
		t.Error("IsReadOnly() = true, want false by default")
	}
}

func TestIsReadOnly_True(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		ReadOnlyMode: true,
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if !g.IsReadOnly() {
		t.Error("IsReadOnly() = false, want true")
	}
}

func TestValidateTaskPaths_AllValid(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{
		filepath.Join(root, "a.txt"),
		filepath.Join(root, "b.txt"),
	}
	if err := g.ValidateTaskPaths(paths); err != nil {
		t.Errorf("ValidateTaskPaths(all valid) = %v, want nil", err)
	}
}

func TestValidateTaskPaths_OneInvalid(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{
		filepath.Join(root, "ok.txt"),
		"/etc/passwd",
	}
	if err := g.ValidateTaskPaths(paths); err == nil {
		t.Error("ValidateTaskPaths(one invalid) = nil, want error")
	}
}

func TestValidateTaskPaths_EmptySlice(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{
		AllowedPaths: []string{root},
	}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	if err := g.ValidateTaskPaths(nil); err != nil {
		t.Errorf("ValidateTaskPaths(empty) = %v, want nil", err)
	}
}

func TestProjectRoot_ReturnsAbsolute(t *testing.T) {
	root := t.TempDir()
	cfg := config.SafetyConfig{}
	g, err := NewGuard(cfg, root)
	if err != nil {
		t.Fatal(err)
	}

	got := g.ProjectRoot()
	if !filepath.IsAbs(got) {
		t.Errorf("ProjectRoot() = %q, want absolute path", got)
	}
	// Should match the resolved root
	expected, _ := filepath.Abs(root)
	if got != expected {
		t.Errorf("ProjectRoot() = %q, want %q", got, expected)
	}
}
