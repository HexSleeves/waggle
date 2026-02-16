package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig_QueenDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Queen.Provider != "anthropic" {
		t.Errorf("Queen.Provider = %q, want %q", cfg.Queen.Provider, "anthropic")
	}
	if cfg.Queen.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Queen.Model = %q, want %q", cfg.Queen.Model, "claude-sonnet-4-20250514")
	}
	if cfg.Queen.MaxIterations != 50 {
		t.Errorf("Queen.MaxIterations = %d, want %d", cfg.Queen.MaxIterations, 50)
	}
	if cfg.Queen.PlanTimeout != 5*time.Minute {
		t.Errorf("Queen.PlanTimeout = %v, want %v", cfg.Queen.PlanTimeout, 5*time.Minute)
	}
	if cfg.Queen.ReviewTimeout != 2*time.Minute {
		t.Errorf("Queen.ReviewTimeout = %v, want %v", cfg.Queen.ReviewTimeout, 2*time.Minute)
	}
	if cfg.Queen.CompactAfter != 100 {
		t.Errorf("Queen.CompactAfter = %d, want %d", cfg.Queen.CompactAfter, 100)
	}
}

func TestDefaultConfig_WorkerDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Workers.MaxParallel != 4 {
		t.Errorf("Workers.MaxParallel = %d, want %d", cfg.Workers.MaxParallel, 4)
	}
	if cfg.Workers.DefaultTimeout != 10*time.Minute {
		t.Errorf("Workers.DefaultTimeout = %v, want %v", cfg.Workers.DefaultTimeout, 10*time.Minute)
	}
	if cfg.Workers.MaxRetries != 2 {
		t.Errorf("Workers.MaxRetries = %d, want %d", cfg.Workers.MaxRetries, 2)
	}
	if cfg.Workers.DefaultAdapter != "claude-code" {
		t.Errorf("Workers.DefaultAdapter = %q, want %q", cfg.Workers.DefaultAdapter, "claude-code")
	}
	if cfg.Workers.MaxOutputSize != 1048576 {
		t.Errorf("Workers.MaxOutputSize = %d, want %d", cfg.Workers.MaxOutputSize, 1048576)
	}
}

func TestDefaultConfig_SafetyDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if len(cfg.Safety.AllowedPaths) != 1 || cfg.Safety.AllowedPaths[0] != "." {
		t.Errorf("Safety.AllowedPaths = %v, want %v", cfg.Safety.AllowedPaths, []string{"."})
	}
	if len(cfg.Safety.BlockedCommands) != 2 {
		t.Fatalf("Safety.BlockedCommands length = %d, want 2", len(cfg.Safety.BlockedCommands))
	}
	if cfg.Safety.BlockedCommands[0] != "rm -rf /" {
		t.Errorf("Safety.BlockedCommands[0] = %q, want %q", cfg.Safety.BlockedCommands[0], "rm -rf /")
	}
	if cfg.Safety.BlockedCommands[1] != "sudo rm" {
		t.Errorf("Safety.BlockedCommands[1] = %q, want %q", cfg.Safety.BlockedCommands[1], "sudo rm")
	}
	if cfg.Safety.ReadOnlyMode {
		t.Error("Safety.ReadOnlyMode should be false by default")
	}
	if cfg.Safety.MaxFileSize != 10485760 {
		t.Errorf("Safety.MaxFileSize = %d, want %d", cfg.Safety.MaxFileSize, 10485760)
	}
}

func TestDefaultConfig_AdapterEntries(t *testing.T) {
	cfg := DefaultConfig()

	expected := []struct {
		name    string
		command string
		args    []string
	}{
		{"claude-code", "claude", []string{"-p"}},
		{"kimi", "kimi", []string{"--print", "--final-message-only", "-p"}},
		{"codex", "codex", []string{"exec"}},
		{"opencode", "opencode", []string{"run"}},
		{"gemini", "gemini", nil},
		{"exec", "bash", nil},
	}

	for _, tc := range expected {
		t.Run(tc.name, func(t *testing.T) {
			a, ok := cfg.Adapters[tc.name]
			if !ok {
				t.Fatalf("adapter %q not found in defaults", tc.name)
			}
			if a.Command != tc.command {
				t.Errorf("adapter %q command = %q, want %q", tc.name, a.Command, tc.command)
			}
			if len(a.Args) != len(tc.args) {
				t.Fatalf("adapter %q args length = %d, want %d", tc.name, len(a.Args), len(tc.args))
			}
			for i, arg := range tc.args {
				if a.Args[i] != arg {
					t.Errorf("adapter %q args[%d] = %q, want %q", tc.name, i, a.Args[i], arg)
				}
			}
		})
	}
}

func TestDefaultConfig_ProjectAndHiveDir(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ProjectDir != "." {
		t.Errorf("ProjectDir = %q, want %q", cfg.ProjectDir, ".")
	}
	if cfg.HiveDir != ".hive" {
		t.Errorf("HiveDir = %q, want %q", cfg.HiveDir, ".hive")
	}
}

func TestLoad_FromJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "waggle.json")

	jsonData := []byte(`{
		"project_dir": "/tmp/myproject",
		"hive_dir": ".custom-hive",
		"queen": {
			"provider": "openai",
			"model": "gpt-4",
			"max_iterations": 25,
			"plan_timeout": 600000000000,
			"review_timeout": 120000000000,
			"compact_after_messages": 50
		},
		"workers": {
			"max_parallel": 8,
			"default_timeout": 300000000000,
			"max_retries": 5,
			"default_adapter": "codex",
			"max_output_size": 2097152
		},
		"safety": {
			"allowed_paths": [".", "/tmp"],
			"blocked_commands": ["rm -rf /"],
			"read_only_mode": true,
			"max_file_size": 5242880
		}
	}`)

	if err := os.WriteFile(path, jsonData, 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.ProjectDir != "/tmp/myproject" {
		t.Errorf("ProjectDir = %q, want %q", cfg.ProjectDir, "/tmp/myproject")
	}
	if cfg.HiveDir != ".custom-hive" {
		t.Errorf("HiveDir = %q, want %q", cfg.HiveDir, ".custom-hive")
	}
	if cfg.Queen.Provider != "openai" {
		t.Errorf("Queen.Provider = %q, want %q", cfg.Queen.Provider, "openai")
	}
	if cfg.Queen.Model != "gpt-4" {
		t.Errorf("Queen.Model = %q, want %q", cfg.Queen.Model, "gpt-4")
	}
	if cfg.Queen.MaxIterations != 25 {
		t.Errorf("Queen.MaxIterations = %d, want %d", cfg.Queen.MaxIterations, 25)
	}
	if cfg.Queen.CompactAfter != 50 {
		t.Errorf("Queen.CompactAfter = %d, want %d", cfg.Queen.CompactAfter, 50)
	}
	if cfg.Workers.MaxParallel != 8 {
		t.Errorf("Workers.MaxParallel = %d, want %d", cfg.Workers.MaxParallel, 8)
	}
	if cfg.Workers.MaxRetries != 5 {
		t.Errorf("Workers.MaxRetries = %d, want %d", cfg.Workers.MaxRetries, 5)
	}
	if cfg.Workers.DefaultAdapter != "codex" {
		t.Errorf("Workers.DefaultAdapter = %q, want %q", cfg.Workers.DefaultAdapter, "codex")
	}
	if cfg.Workers.MaxOutputSize != 2097152 {
		t.Errorf("Workers.MaxOutputSize = %d, want %d", cfg.Workers.MaxOutputSize, 2097152)
	}
	if cfg.Safety.ReadOnlyMode != true {
		t.Error("Safety.ReadOnlyMode should be true")
	}
	if cfg.Safety.MaxFileSize != 5242880 {
		t.Errorf("Safety.MaxFileSize = %d, want %d", cfg.Safety.MaxFileSize, 5242880)
	}
	if len(cfg.Safety.AllowedPaths) != 2 {
		t.Errorf("Safety.AllowedPaths length = %d, want 2", len(cfg.Safety.AllowedPaths))
	}
}

func TestLoad_NonexistentFile_ReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error for nonexistent file: %v", err)
	}

	defCfg := DefaultConfig()

	if cfg.Queen.Provider != defCfg.Queen.Provider {
		t.Errorf("Queen.Provider = %q, want default %q", cfg.Queen.Provider, defCfg.Queen.Provider)
	}
	if cfg.Queen.Model != defCfg.Queen.Model {
		t.Errorf("Queen.Model = %q, want default %q", cfg.Queen.Model, defCfg.Queen.Model)
	}
	if cfg.Workers.MaxParallel != defCfg.Workers.MaxParallel {
		t.Errorf("Workers.MaxParallel = %d, want default %d", cfg.Workers.MaxParallel, defCfg.Workers.MaxParallel)
	}
	if cfg.Workers.DefaultAdapter != defCfg.Workers.DefaultAdapter {
		t.Errorf("Workers.DefaultAdapter = %q, want default %q", cfg.Workers.DefaultAdapter, defCfg.Workers.DefaultAdapter)
	}
}

func TestLoad_InvalidJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	if err := os.WriteFile(path, []byte(`{invalid json!!!`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should return error for invalid JSON, got nil")
	}
}

func TestSaveAndLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.json")

	original := DefaultConfig()
	original.ProjectDir = "/my/project"
	original.HiveDir = ".custom"
	original.Queen.Provider = "openai"
	original.Queen.Model = "gpt-4o"
	original.Queen.MaxIterations = 99
	original.Queen.CompactAfter = 200
	original.Workers.MaxParallel = 16
	original.Workers.DefaultAdapter = "gemini"
	original.Workers.MaxOutputSize = 5000000
	original.Workers.MaxRetries = 10
	original.Safety.ReadOnlyMode = true
	original.Safety.MaxFileSize = 999
	original.Safety.AllowedPaths = []string{"/a", "/b"}
	original.Safety.BlockedCommands = []string{"danger"}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Queen fields
	if loaded.Queen.Provider != original.Queen.Provider {
		t.Errorf("Queen.Provider = %q, want %q", loaded.Queen.Provider, original.Queen.Provider)
	}
	if loaded.Queen.Model != original.Queen.Model {
		t.Errorf("Queen.Model = %q, want %q", loaded.Queen.Model, original.Queen.Model)
	}
	if loaded.Queen.MaxIterations != original.Queen.MaxIterations {
		t.Errorf("Queen.MaxIterations = %d, want %d", loaded.Queen.MaxIterations, original.Queen.MaxIterations)
	}
	if loaded.Queen.CompactAfter != original.Queen.CompactAfter {
		t.Errorf("Queen.CompactAfter = %d, want %d", loaded.Queen.CompactAfter, original.Queen.CompactAfter)
	}
	if loaded.Queen.PlanTimeout != original.Queen.PlanTimeout {
		t.Errorf("Queen.PlanTimeout = %v, want %v", loaded.Queen.PlanTimeout, original.Queen.PlanTimeout)
	}
	if loaded.Queen.ReviewTimeout != original.Queen.ReviewTimeout {
		t.Errorf("Queen.ReviewTimeout = %v, want %v", loaded.Queen.ReviewTimeout, original.Queen.ReviewTimeout)
	}

	// Worker fields
	if loaded.Workers.MaxParallel != original.Workers.MaxParallel {
		t.Errorf("Workers.MaxParallel = %d, want %d", loaded.Workers.MaxParallel, original.Workers.MaxParallel)
	}
	if loaded.Workers.DefaultAdapter != original.Workers.DefaultAdapter {
		t.Errorf("Workers.DefaultAdapter = %q, want %q", loaded.Workers.DefaultAdapter, original.Workers.DefaultAdapter)
	}
	if loaded.Workers.MaxOutputSize != original.Workers.MaxOutputSize {
		t.Errorf("Workers.MaxOutputSize = %d, want %d", loaded.Workers.MaxOutputSize, original.Workers.MaxOutputSize)
	}
	if loaded.Workers.MaxRetries != original.Workers.MaxRetries {
		t.Errorf("Workers.MaxRetries = %d, want %d", loaded.Workers.MaxRetries, original.Workers.MaxRetries)
	}

	// Project fields
	if loaded.ProjectDir != original.ProjectDir {
		t.Errorf("ProjectDir = %q, want %q", loaded.ProjectDir, original.ProjectDir)
	}
	if loaded.HiveDir != original.HiveDir {
		t.Errorf("HiveDir = %q, want %q", loaded.HiveDir, original.HiveDir)
	}

	// Safety fields
	if loaded.Safety.ReadOnlyMode != original.Safety.ReadOnlyMode {
		t.Errorf("Safety.ReadOnlyMode = %v, want %v", loaded.Safety.ReadOnlyMode, original.Safety.ReadOnlyMode)
	}
	if loaded.Safety.MaxFileSize != original.Safety.MaxFileSize {
		t.Errorf("Safety.MaxFileSize = %d, want %d", loaded.Safety.MaxFileSize, original.Safety.MaxFileSize)
	}
	if len(loaded.Safety.AllowedPaths) != len(original.Safety.AllowedPaths) {
		t.Errorf("Safety.AllowedPaths length = %d, want %d", len(loaded.Safety.AllowedPaths), len(original.Safety.AllowedPaths))
	}
	if len(loaded.Safety.BlockedCommands) != len(original.Safety.BlockedCommands) {
		t.Errorf("Safety.BlockedCommands length = %d, want %d", len(loaded.Safety.BlockedCommands), len(original.Safety.BlockedCommands))
	}
}

func TestHivePath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ProjectDir = "/myproject"
	cfg.HiveDir = ".hive"

	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{"no parts", nil, "/myproject/.hive"},
		{"single part", []string{"tasks"}, "/myproject/.hive/tasks"},
		{"multiple parts", []string{"tasks", "task-1", "output.txt"}, "/myproject/.hive/tasks/task-1/output.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.HivePath(tc.parts...)
			if got != tc.want {
				t.Errorf("HivePath(%v) = %q, want %q", tc.parts, got, tc.want)
			}
		})
	}
}

func TestIsQuiet(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.IsQuiet() {
		t.Error("IsQuiet() should be false by default")
	}
	cfg.Output.Quiet = true
	if !cfg.IsQuiet() {
		t.Error("IsQuiet() should be true after setting Output.Quiet")
	}
}

func TestIsJSON(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.IsJSON() {
		t.Error("IsJSON() should be false by default")
	}
	cfg.Output.JSON = true
	if !cfg.IsJSON() {
		t.Error("IsJSON() should be true after setting Output.JSON")
	}
}

func TestIsPlain(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.IsPlain() {
		t.Error("IsPlain() should be false by default")
	}
	cfg.Output.Plain = true
	if !cfg.IsPlain() {
		t.Error("IsPlain() should be true after setting Output.Plain")
	}
}

func TestConfigAdapterMapParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "waggle.json")

	// Save config with adapter_map
	original := DefaultConfig()
	original.Workers.AdapterMap = map[string]string{
		"code":   "kimi",
		"test":   "exec",
		"review": "claude-code",
	}
	if err := original.Save(path); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Load it back
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loaded.Workers.AdapterMap) != 3 {
		t.Fatalf("AdapterMap length = %d, want 3", len(loaded.Workers.AdapterMap))
	}

	expected := map[string]string{
		"code":   "kimi",
		"test":   "exec",
		"review": "claude-code",
	}
	for k, v := range expected {
		if loaded.Workers.AdapterMap[k] != v {
			t.Errorf("AdapterMap[%q] = %q, want %q", k, loaded.Workers.AdapterMap[k], v)
		}
	}
}

func TestConfigAdapterMapOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "waggle.json")

	// Save config without adapter_map
	cfg := DefaultConfig()
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify the JSON doesn't contain adapter_map
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	contents := string(data)
	if filepath.Ext(path) == ".json" {
		// adapter_map should not appear in JSON when nil/empty
		for _, substr := range []string{"adapter_map"} {
			if len(contents) > 0 {
				found := false
				for i := 0; i <= len(contents)-len(substr); i++ {
					if contents[i:i+len(substr)] == substr {
						found = true
						break
					}
				}
				if found {
					t.Error("adapter_map should be omitted from JSON when empty")
				}
			}
		}
	}

	// Load back and verify nil
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Workers.AdapterMap != nil {
		t.Errorf("AdapterMap should be nil when not set, got %v", loaded.Workers.AdapterMap)
	}
}
