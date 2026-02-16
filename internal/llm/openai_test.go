package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClient_ChatWithHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request structure
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json content-type")
		}

		body, _ := io.ReadAll(r.Body)
		var req openaiRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("expected model test-model, got %s", req.Model)
		}
		// system + user + assistant + user = 4 messages
		if len(req.Messages) != 4 {
			t.Errorf("expected 4 messages, got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" {
			t.Errorf("expected system role first, got %s", req.Messages[0].Role)
		}

		resp := openaiResponse{
			Choices: []openaiChoice{{
				Message:      openaiMessage{Role: "assistant", Content: "Hello there!"},
				FinishReason: "stop",
			}},
			Usage: &openaiUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("test-key", "test-model", server.URL)
	result, err := client.ChatWithHistory(context.Background(), "You are helpful.", []Message{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello"},
		{Role: "user", Content: "How are you?"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello there!" {
		t.Errorf("expected 'Hello there!', got %q", result)
	}
}

func TestOpenAIClient_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req openaiRequest
		json.Unmarshal(body, &req)
		// Chat wraps into single user message + system
		if len(req.Messages) != 2 {
			t.Errorf("expected 2 messages, got %d", len(req.Messages))
		}
		resp := openaiResponse{
			Choices: []openaiChoice{{
				Message:      openaiMessage{Role: "assistant", Content: "response"},
				FinishReason: "stop",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	result, err := client.Chat(context.Background(), "system", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "response" {
		t.Errorf("expected 'response', got %q", result)
	}
}

func TestOpenAIClient_ChatWithTools_Serialization(t *testing.T) {
	var capturedReq openaiRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := openaiResponse{
			Choices: []openaiChoice{{
				Message: openaiMessage{
					Role:    "assistant",
					Content: "I'll help you.",
				},
				FinishReason: "stop",
			}},
			Usage: &openaiUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			Model: "gpt-4o-2024-01-01",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("test-key", "gpt-4o", server.URL)

	messages := []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Do something"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "text", Text: "Sure"},
			{Type: "tool_use", ToolCall: &ToolCall{ID: "tc-1", Name: "read_file", Input: json.RawMessage(`{"path":"main.go"}`)}},
		}},
		{Role: "tool_result", ToolResults: []ToolResult{{ToolCallID: "tc-1", Content: "file contents"}}},
	}
	tools := []ToolDef{{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}},
	}}

	resp, err := client.ChatWithTools(context.Background(), "You are a bot.", messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify request
	if capturedReq.Model != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %s", capturedReq.Model)
	}
	if len(capturedReq.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(capturedReq.Tools))
	}
	if capturedReq.Tools[0].Function.Name != "read_file" {
		t.Errorf("expected tool name read_file, got %s", capturedReq.Tools[0].Function.Name)
	}
	// system + user + assistant(with tool_calls) + tool = 4 messages
	if len(capturedReq.Messages) != 4 {
		t.Errorf("expected 4 messages, got %d", len(capturedReq.Messages))
	}
	// Last message should be role=tool
	last := capturedReq.Messages[len(capturedReq.Messages)-1]
	if last.Role != "tool" {
		t.Errorf("expected last message role=tool, got %s", last.Role)
	}

	// Verify response parsing
	if resp.StopReason != "end_turn" {
		t.Errorf("expected stop reason end_turn, got %s", resp.StopReason)
	}
	if resp.Model != "gpt-4o-2024-01-01" {
		t.Errorf("expected model gpt-4o-2024-01-01, got %s", resp.Model)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", resp.Usage.OutputTokens)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "I'll help you." {
		t.Errorf("expected text content 'I'll help you.', got %+v", resp.Content)
	}
}

func TestOpenAIClient_ToolCallParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Choices: []openaiChoice{{
				Message: openaiMessage{
					Role: "assistant",
					ToolCalls: []openaiToolCall{
						{
							ID:   "call_abc",
							Type: "function",
							Function: openaiCallFunction{
								Name:      "get_status",
								Arguments: `{"verbose":true}`,
							},
						},
						{
							ID:   "call_def",
							Type: "function",
							Function: openaiCallFunction{
								Name:      "read_file",
								Arguments: `{"path":"go.mod"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	resp, err := client.ChatWithTools(context.Background(), "", []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Go"}}},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Errorf("expected stop reason tool_use, got %s", resp.StopReason)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(resp.Content))
	}
	for i, tc := range resp.Content {
		if tc.Type != "tool_use" {
			t.Errorf("block %d: expected tool_use, got %s", i, tc.Type)
		}
		if tc.ToolCall == nil {
			t.Fatalf("block %d: nil ToolCall", i)
		}
	}
	if resp.Content[0].ToolCall.Name != "get_status" {
		t.Errorf("expected get_status, got %s", resp.Content[0].ToolCall.Name)
	}
	if resp.Content[1].ToolCall.ID != "call_def" {
		t.Errorf("expected call_def, got %s", resp.Content[1].ToolCall.ID)
	}
}

func TestOpenAIClient_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"internal server error","type":"server_error"}}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	_, err := client.Chat(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestOpenAIClient_RateLimitError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit"}}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	_, err := client.Chat(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected 429 in error, got: %v", err)
	}
}

func TestOpenAIClient_MaxTokensStopReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Choices: []openaiChoice{{
				Message:      openaiMessage{Role: "assistant", Content: "partial..."},
				FinishReason: "length",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	resp, err := client.ChatWithTools(context.Background(), "", []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Go"}}},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StopReason != "max_tokens" {
		t.Errorf("expected max_tokens, got %s", resp.StopReason)
	}
}

func TestOpenAIClient_NoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{Choices: []openaiChoice{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	_, err := client.Chat(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error for no choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("expected 'no choices' error, got: %v", err)
	}
}

func TestOpenAIClient_NoChoicesToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{Choices: []openaiChoice{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	_, err := client.ChatWithTools(context.Background(), "", []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Go"}}},
	}, nil)
	if err == nil {
		t.Fatal("expected error for no choices")
	}
}

func TestOpenAIClient_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Choices: []openaiChoice{},
			Error:   &openaiError{Message: "bad request", Type: "invalid_request"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	_, err := client.Chat(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Errorf("expected 'bad request' in error, got: %v", err)
	}
}

func TestOpenAIClient_ChatWithHistory_NoSystem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req openaiRequest
		json.Unmarshal(body, &req)
		// No system prompt → just 1 user message
		if len(req.Messages) != 1 {
			t.Errorf("expected 1 message (no system), got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "user" {
			t.Errorf("expected user role, got %s", req.Messages[0].Role)
		}
		resp := openaiResponse{
			Choices: []openaiChoice{{
				Message:      openaiMessage{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("key", "model", server.URL)
	_, err := client.ChatWithHistory(context.Background(), "", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewOpenAIClient_Defaults(t *testing.T) {
	c := NewOpenAIClient("", "", "")
	if c.model != "gpt-4o" {
		t.Errorf("expected default model gpt-4o, got %s", c.model)
	}
	if c.baseURL != "https://api.openai.com/v1" {
		t.Errorf("expected default baseURL, got %s", c.baseURL)
	}
}

func TestNewOpenAIClient_TrailingSlash(t *testing.T) {
	c := NewOpenAIClient("key", "model", "http://example.com/v1/")
	if strings.HasSuffix(c.baseURL, "/") {
		t.Errorf("baseURL should not have trailing slash: %s", c.baseURL)
	}
}

func TestOpenAIClient_InterfaceCompliance(t *testing.T) {
	var _ Client = (*OpenAIClient)(nil)
	var _ ToolClient = (*OpenAIClient)(nil)
}
