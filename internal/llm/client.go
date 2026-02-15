// Package llm provides a provider-agnostic interface for LLM calls.
package llm

import "context"

// Message represents a single conversation turn.
type Message struct {
	Role    string
	Content string
}

// Client is the interface the Queen uses for reasoning (review, replan).
// Implementations exist for Anthropic, OpenAI, Gemini, and CLI adapters.
type Client interface {
	Chat(ctx context.Context, systemPrompt, userMessage string) (string, error)
	ChatWithHistory(ctx context.Context, systemPrompt string, messages []Message) (string, error)
}

// ToolClient extends Client with tool-use capability.
// Providers that support structured tool calls (like Anthropic) implement this.
type ToolClient interface {
	Client
	ChatWithTools(ctx context.Context, systemPrompt string,
		messages []ToolMessage, tools []ToolDef) (*Response, error)
}
