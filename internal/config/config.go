package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	// Project settings
	ProjectDir string `json:"project_dir"`
	HiveDir    string `json:"hive_dir"`

	// Queen settings
	Queen QueenConfig `json:"queen"`

	// Worker settings
	Workers WorkerConfig `json:"workers"`

	// Adapter configs
	Adapters map[string]AdapterConfig `json:"adapters"`

	// Safety settings
	Safety SafetyConfig `json:"safety"`
}

type QueenConfig struct {
	Model         string        `json:"model"`
	Provider      string        `json:"provider"`
	APIKey        string        `json:"api_key,omitempty"`
	MaxIterations int           `json:"max_iterations"`
	PlanTimeout   time.Duration `json:"plan_timeout"`
	ReviewTimeout time.Duration `json:"review_timeout"`
	CompactAfter  int           `json:"compact_after_messages"`
}

type WorkerConfig struct {
	MaxParallel    int           `json:"max_parallel"`
	DefaultTimeout time.Duration `json:"default_timeout"`
	MaxRetries     int           `json:"max_retries"`
	DefaultAdapter string        `json:"default_adapter"`
}

type AdapterConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"`
	Timeout time.Duration     `json:"timeout,omitempty"`
}

type SafetyConfig struct {
	AllowedPaths    []string `json:"allowed_paths"`
	BlockedCommands []string `json:"blocked_commands"`
	ReadOnlyMode    bool     `json:"read_only_mode"`
	MaxFileSize     int64    `json:"max_file_size"`
}

func DefaultConfig() *Config {
	return &Config{
		ProjectDir: ".",
		HiveDir:    ".hive",
		Queen: QueenConfig{
			Provider:      "anthropic",
			Model:         "claude-sonnet-4-20250514",
			MaxIterations: 50,
			PlanTimeout:   5 * time.Minute,
			ReviewTimeout: 2 * time.Minute,
			CompactAfter:  100,
		},
		Workers: WorkerConfig{
			MaxParallel:    4,
			DefaultTimeout: 10 * time.Minute,
			MaxRetries:     2,
			DefaultAdapter: "claude-code",
		},
		Adapters: map[string]AdapterConfig{
			"claude-code": {
				Command: "claude",
				Args:    []string{"-p"},
			},
			"codex": {
				Command: "codex",
				Args:    []string{"exec"},
			},
			"opencode": {
				Command: "opencode",
				Args:    []string{"run"},
			},
			"kimi": {
				Command: "kimi",
				Args:    []string{"--print", "--final-message-only", "-p"},
			},
			"gemini": {
				Command: "gemini",
			},
			"exec": {
				Command: "bash",
			},
		},
		Safety: SafetyConfig{
			AllowedPaths:    []string{"."},
			BlockedCommands: []string{"rm -rf /", "sudo rm"},
			MaxFileSize:     10 * 1024 * 1024,
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) HivePath(parts ...string) string {
	elems := append([]string{c.ProjectDir, c.HiveDir}, parts...)
	return filepath.Join(elems...)
}

func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
