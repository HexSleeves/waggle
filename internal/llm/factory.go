package llm

import "fmt"

// ProviderConfig holds what's needed to construct an LLM client.
type ProviderConfig struct {
	Provider string // "anthropic", "openai", "codex", "gemini", "kimi", "claude-cli", etc.
	Model    string
	APIKey   string
	BaseURL  string // optional: override API base URL (for OpenAI-compatible endpoints)
	WorkDir  string // for CLI-based providers
}

// NewFromConfig creates the appropriate Client based on provider name.
// API-based providers (anthropic, openai, codex, gemini-api) support tool use (ToolClient).
// CLI-based providers (kimi, claude-cli, gemini, opencode) only support basic chat (Client).
func NewFromConfig(cfg ProviderConfig) (Client, error) {
	switch cfg.Provider {

	// === API-based providers (support tool use → agent mode) ===

	case "anthropic":
		return NewAnthropicClient(cfg.APIKey, cfg.Model), nil

	case "openai":
		return NewOpenAIClient(cfg.APIKey, cfg.Model, cfg.BaseURL), nil

	case "codex":
		// Codex uses the OpenAI API
		model := cfg.Model
		if model == "" {
			model = "codex-mini-latest"
		}
		return NewOpenAIClient(cfg.APIKey, model, cfg.BaseURL), nil

	case "gemini-api", "google":
		return NewGeminiClient(cfg.APIKey, cfg.Model), nil

	// === CLI-based providers (basic chat only → legacy mode) ===

	case "kimi":
		return NewCLIClient("kimi", []string{"--print", "--final-message-only", "-p"}, cfg.WorkDir, false), nil

	case "claude-cli", "claude-code":
		return NewCLIClient("claude", []string{"-p"}, cfg.WorkDir, false), nil

	case "gemini":
		// CLI-based gemini (piped). For API-based, use "gemini-api".
		return NewCLIClient("gemini", nil, cfg.WorkDir, true), nil

	case "opencode":
		return NewCLIClient("opencode", []string{"run"}, cfg.WorkDir, false), nil

	case "":
		return nil, fmt.Errorf("no LLM provider configured (set queen.provider in waggle.json)")

	default:
		return nil, fmt.Errorf("unknown LLM provider: %q (supported: anthropic, openai, codex, gemini-api, gemini, kimi, claude-cli, opencode)", cfg.Provider)
	}
}
