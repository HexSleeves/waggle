package llm

import "testing"

func TestAnthropicClient(t *testing.T) {
	c := NewAnthropicClient("test-key", "")
	if c == nil {
		t.Fatal("NewAnthropicClient returned nil")
	}
	if c.model != "claude-sonnet-4-20250514" {
		t.Fatalf("expected default model, got %q", c.model)
	}
}

func TestAnthropicClientCustomModel(t *testing.T) {
	c := NewAnthropicClient("test-key", "claude-haiku-3-20240307")
	if c.model != "claude-haiku-3-20240307" {
		t.Fatalf("expected custom model, got %q", c.model)
	}
}

func TestCLIClient(t *testing.T) {
	c := NewCLIClient("echo", []string{"-n"}, "", false)
	if c == nil {
		t.Fatal("NewCLIClient returned nil")
	}
	if c.command != "echo" {
		t.Fatalf("expected echo, got %q", c.command)
	}
}

func TestCLIClientPipe(t *testing.T) {
	c := NewCLIClient("cat", nil, "", true)
	if !c.pipe {
		t.Fatal("expected pipe mode")
	}
}

func TestNewFromConfig_Anthropic(t *testing.T) {
	c, err := NewFromConfig(ProviderConfig{Provider: "anthropic", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewFromConfig_Kimi(t *testing.T) {
	c, err := NewFromConfig(ProviderConfig{Provider: "kimi", WorkDir: "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewFromConfig_Gemini(t *testing.T) {
	c, err := NewFromConfig(ProviderConfig{Provider: "gemini"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewFromConfig_Empty(t *testing.T) {
	_, err := NewFromConfig(ProviderConfig{})
	if err == nil {
		t.Fatal("expected error for empty provider")
	}
}

func TestNewFromConfig_Unknown(t *testing.T) {
	_, err := NewFromConfig(ProviderConfig{Provider: "doesnotexist"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestInterfaceCompliance(t *testing.T) {
	// Verify both implementations satisfy the Client interface
	var _ Client = (*AnthropicClient)(nil)
	var _ Client = (*CLIClient)(nil)

	// Verify AnthropicClient satisfies ToolClient
	var _ ToolClient = (*AnthropicClient)(nil)
}

func TestUsageInResponse(t *testing.T) {
	// Verify Usage struct is populated correctly in a Response
	resp := &Response{
		Content:    []ContentBlock{{Type: "text", Text: "hello"}},
		StopReason: "end_turn",
		Model:      "test-model",
		Usage: Usage{
			InputTokens:         1000,
			OutputTokens:        200,
			CacheCreationTokens: 500,
			CacheReadTokens:     800,
		},
	}

	if resp.Usage.InputTokens != 1000 {
		t.Errorf("expected InputTokens=1000, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 200 {
		t.Errorf("expected OutputTokens=200, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.CacheCreationTokens != 500 {
		t.Errorf("expected CacheCreationTokens=500, got %d", resp.Usage.CacheCreationTokens)
	}
	if resp.Usage.CacheReadTokens != 800 {
		t.Errorf("expected CacheReadTokens=800, got %d", resp.Usage.CacheReadTokens)
	}
	if resp.Model != "test-model" {
		t.Errorf("expected Model=test-model, got %q", resp.Model)
	}
}

func TestUsageZeroValue(t *testing.T) {
	// Zero-value Usage should have all fields at 0
	var u Usage
	if u.InputTokens != 0 || u.OutputTokens != 0 || u.CacheCreationTokens != 0 || u.CacheReadTokens != 0 {
		t.Error("zero-value Usage should have all fields at 0")
	}
}

func TestContentBlockCacheField(t *testing.T) {
	// Verify Cache field on ContentBlock
	cb := ContentBlock{Type: "text", Text: "hello", Cache: true}
	if !cb.Cache {
		t.Error("expected Cache=true")
	}
}

func TestToolDefCacheField(t *testing.T) {
	// Verify Cache field on ToolDef
	td := ToolDef{Name: "test", Cache: true}
	if !td.Cache {
		t.Error("expected Cache=true")
	}
}
