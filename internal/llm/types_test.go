package llm

import (
	"encoding/json"
	"testing"
)

func TestToolMessage_JSONRoundTrip(t *testing.T) {
	original := ToolMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: "Hello"},
			{Type: "tool_use", ToolCall: &ToolCall{ID: "tc-1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)}},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ToolMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Role != original.Role {
		t.Errorf("role: expected %s, got %s", original.Role, decoded.Role)
	}
	if len(decoded.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(decoded.Content))
	}
	if decoded.Content[0].Text != "Hello" {
		t.Errorf("expected Hello, got %s", decoded.Content[0].Text)
	}
	if decoded.Content[1].ToolCall == nil {
		t.Fatal("expected non-nil ToolCall")
	}
	if decoded.Content[1].ToolCall.Name != "read_file" {
		t.Errorf("expected read_file, got %s", decoded.Content[1].ToolCall.Name)
	}
}

func TestToolMessage_ToolResult_JSONRoundTrip(t *testing.T) {
	original := ToolMessage{
		Role: "tool_result",
		ToolResults: []ToolResult{
			{ToolCallID: "tc-1", Content: "output here", IsError: false},
			{ToolCallID: "tc-2", Content: "error occurred", IsError: true},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ToolMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.ToolResults) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(decoded.ToolResults))
	}
	if decoded.ToolResults[0].ToolCallID != "tc-1" {
		t.Errorf("expected tc-1, got %s", decoded.ToolResults[0].ToolCallID)
	}
	if decoded.ToolResults[1].IsError != true {
		t.Error("expected IsError=true for second result")
	}
}

func TestResponse_JSONRoundTrip(t *testing.T) {
	original := Response{
		Content: []ContentBlock{
			{Type: "text", Text: "I'll do that."},
			{Type: "tool_use", ToolCall: &ToolCall{ID: "call-1", Name: "get_status", Input: json.RawMessage(`{}`)}},
		},
		StopReason: "tool_use",
		Usage:      Usage{InputTokens: 1000, OutputTokens: 200, CacheCreationTokens: 50, CacheReadTokens: 800},
		Model:      "claude-3",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.StopReason != "tool_use" {
		t.Errorf("expected tool_use, got %s", decoded.StopReason)
	}
	if decoded.Model != "claude-3" {
		t.Errorf("expected claude-3, got %s", decoded.Model)
	}
	if decoded.Usage.InputTokens != 1000 {
		t.Errorf("expected 1000, got %d", decoded.Usage.InputTokens)
	}
	if decoded.Usage.CacheReadTokens != 800 {
		t.Errorf("expected 800, got %d", decoded.Usage.CacheReadTokens)
	}
	if len(decoded.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(decoded.Content))
	}
	if decoded.Content[1].ToolCall.Name != "get_status" {
		t.Errorf("expected get_status, got %s", decoded.Content[1].ToolCall.Name)
	}
}

func TestUsage_JSONRoundTrip(t *testing.T) {
	original := Usage{
		InputTokens:         500,
		OutputTokens:        100,
		CacheCreationTokens: 25,
		CacheReadTokens:     300,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Usage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded != original {
		t.Errorf("expected %+v, got %+v", original, decoded)
	}
}

func TestToolDef_JSONRoundTrip(t *testing.T) {
	original := ToolDef{
		Name:        "create_tasks",
		Description: "Create tasks",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Cache:       true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ToolDef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Name != "create_tasks" {
		t.Errorf("expected create_tasks, got %s", decoded.Name)
	}
	if !decoded.Cache {
		t.Error("expected Cache=true")
	}
}

func TestToolCall_JSONRoundTrip(t *testing.T) {
	original := ToolCall{
		ID:    "call-abc",
		Name:  "read_file",
		Input: json.RawMessage(`{"path":"main.go","line_start":1}`),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ToolCall
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != "call-abc" {
		t.Errorf("expected call-abc, got %s", decoded.ID)
	}
	if decoded.Name != "read_file" {
		t.Errorf("expected read_file, got %s", decoded.Name)
	}
	if string(decoded.Input) != string(original.Input) {
		t.Errorf("input mismatch: %s vs %s", decoded.Input, original.Input)
	}
}

func TestContentBlock_OmitEmpty(t *testing.T) {
	// Text-only block should omit tool_call
	cb := ContentBlock{Type: "text", Text: "hello"}
	data, _ := json.Marshal(cb)
	str := string(data)
	if str == "" {
		t.Fatal("empty marshal")
	}
	// tool_call should be omitted
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if _, ok := m["tool_call"]; ok {
		t.Error("tool_call should be omitted for text-only block")
	}
}

func TestToolResult_JSONRoundTrip(t *testing.T) {
	original := ToolResult{
		ToolCallID: "tc-42",
		Content:    "operation succeeded",
		IsError:    false,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ToolResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ToolCallID != "tc-42" {
		t.Errorf("expected tc-42, got %s", decoded.ToolCallID)
	}
	if decoded.Content != "operation succeeded" {
		t.Errorf("expected 'operation succeeded', got %s", decoded.Content)
	}
	if decoded.IsError {
		t.Error("expected IsError=false")
	}

	// IsError omitted when false
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if _, ok := m["is_error"]; ok {
		t.Error("is_error should be omitted when false")
	}
}
