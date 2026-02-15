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
