package llm

import "encoding/json"

// ToolDef defines a tool the LLM can call.
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ToolCall represents the LLM requesting a tool invocation.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is the response to a tool call.
type ToolResult struct {
	ToolCallID string `json:"tool_use_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// ContentBlock is a single block in a message (text or tool_use).
type ContentBlock struct {
	Type     string    `json:"type"` // "text" or "tool_use"
	Text     string    `json:"text,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
}

// Response is the LLM's response, possibly containing tool calls.
type Response struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"` // "end_turn", "tool_use", "max_tokens"
}

// ToolMessage is a rich message that can contain text, tool calls, or tool results.
type ToolMessage struct {
	Role        string         `json:"role"` // "user", "assistant", "tool_result"
	Content     []ContentBlock `json:"content,omitempty"`
	ToolResults []ToolResult   `json:"tool_results,omitempty"`
}
