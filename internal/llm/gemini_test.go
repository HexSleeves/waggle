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

func TestGeminiClient_ChatWithHistory(t *testing.T) {
	var capturedReq geminiRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "generateContent") {
			t.Errorf("expected generateContent in path, got %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("expected x-goog-api-key=test-key, got %s", r.Header.Get("x-goog-api-key"))
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content: geminiContent{
					Role:  "model",
					Parts: []geminiPart{{Text: "Hello from Gemini!"}},
				},
				FinishReason: "STOP",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("test-key", "gemini-2.5-flash")
	client.baseURL = server.URL

	result, err := client.ChatWithHistory(context.Background(), "Be helpful.", []Message{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello"},
		{Role: "user", Content: "How are you?"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from Gemini!" {
		t.Errorf("expected 'Hello from Gemini!', got %q", result)
	}

	// Verify system instruction
	if capturedReq.SystemInstruction == nil {
		t.Fatal("expected system instruction")
	}
	if capturedReq.SystemInstruction.Parts[0].Text != "Be helpful." {
		t.Errorf("expected system text, got %s", capturedReq.SystemInstruction.Parts[0].Text)
	}
	// user + model + user = 3 contents
	if len(capturedReq.Contents) != 3 {
		t.Errorf("expected 3 contents, got %d", len(capturedReq.Contents))
	}
	if capturedReq.Contents[1].Role != "model" {
		t.Errorf("expected model role for assistant, got %s", capturedReq.Contents[1].Role)
	}
}

func TestGeminiClient_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content: geminiContent{
					Role:  "model",
					Parts: []geminiPart{{Text: "ok"}},
				},
				FinishReason: "STOP",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL
	result, err := client.Chat(context.Background(), "sys", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestGeminiClient_ChatWithTools_Serialization(t *testing.T) {
	var capturedReq geminiRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content: geminiContent{
					Role:  "model",
					Parts: []geminiPart{{Text: "I understand."}},
				},
				FinishReason: "STOP",
			}},
			UsageMetadata: &geminiUsage{PromptTokenCount: 50, CandidatesTokenCount: 20, TotalTokenCount: 70},
			ModelVersion:  "gemini-2.5-flash-001",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("key", "gemini-2.5-flash")
	client.baseURL = server.URL

	messages := []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Do something"}}},
	}
	tools := []ToolDef{{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}},
	}}

	resp, err := client.ChatWithTools(context.Background(), "system", messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify request structure
	if len(capturedReq.Tools) != 1 {
		t.Fatalf("expected 1 tool group, got %d", len(capturedReq.Tools))
	}
	if len(capturedReq.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected 1 function declaration, got %d", len(capturedReq.Tools[0].FunctionDeclarations))
	}
	if capturedReq.Tools[0].FunctionDeclarations[0].Name != "read_file" {
		t.Errorf("expected read_file, got %s", capturedReq.Tools[0].FunctionDeclarations[0].Name)
	}

	// Verify response
	if resp.StopReason != "end_turn" {
		t.Errorf("expected end_turn, got %s", resp.StopReason)
	}
	if resp.Model != "gemini-2.5-flash-001" {
		t.Errorf("expected model version, got %s", resp.Model)
	}
	if resp.Usage.InputTokens != 50 {
		t.Errorf("expected 50 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 20 {
		t.Errorf("expected 20 output tokens, got %d", resp.Usage.OutputTokens)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "I understand." {
		t.Errorf("unexpected content: %+v", resp.Content)
	}
}

func TestGeminiClient_FunctionCallParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content: geminiContent{
					Role: "model",
					Parts: []geminiPart{
						{Text: "Let me check."},
						{FunctionCall: &geminiFunctionCall{Name: "get_status", Args: map[string]interface{}{"verbose": true}}},
						{FunctionCall: &geminiFunctionCall{Name: "read_file", Args: map[string]interface{}{"path": "main.go"}}},
					},
				},
				FinishReason: "STOP",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL

	resp, err := client.ChatWithTools(context.Background(), "", []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Go"}}},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Errorf("expected tool_use stop reason, got %s", resp.StopReason)
	}
	if len(resp.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(resp.Content))
	}
	// First: text
	if resp.Content[0].Type != "text" || resp.Content[0].Text != "Let me check." {
		t.Errorf("expected text block, got %+v", resp.Content[0])
	}
	// Second: tool_use
	if resp.Content[1].Type != "tool_use" {
		t.Errorf("expected tool_use, got %s", resp.Content[1].Type)
	}
	if resp.Content[1].ToolCall.Name != "get_status" {
		t.Errorf("expected get_status, got %s", resp.Content[1].ToolCall.Name)
	}
	if resp.Content[1].ToolCall.ID != "gemini-call-0" {
		t.Errorf("expected gemini-call-0, got %s", resp.Content[1].ToolCall.ID)
	}
	// Third: tool_use
	if resp.Content[2].ToolCall.Name != "read_file" {
		t.Errorf("expected read_file, got %s", resp.Content[2].ToolCall.Name)
	}
	if resp.Content[2].ToolCall.ID != "gemini-call-1" {
		t.Errorf("expected gemini-call-1, got %s", resp.Content[2].ToolCall.ID)
	}
}

func TestGeminiClient_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"code":500,"message":"internal error"}}`))
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL
	_, err := client.Chat(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestGeminiClient_RateLimitError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"code":429,"message":"rate limited"}}`))
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL
	_, err := client.Chat(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected 429 in error, got: %v", err)
	}
}

func TestGeminiClient_NoCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{Candidates: []geminiCandidate{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL
	_, err := client.Chat(context.Background(), "", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no candidates") {
		t.Errorf("expected 'no candidates', got: %v", err)
	}
}

func TestGeminiClient_NoCandidatesToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{Candidates: []geminiCandidate{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL
	_, err := client.ChatWithTools(context.Background(), "", []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Go"}}},
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGeminiClient_MaxTokensStopReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content:      geminiContent{Role: "model", Parts: []geminiPart{{Text: "partial"}}},
				FinishReason: "MAX_TOKENS",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL
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

func TestGeminiClient_FindFunctionName(t *testing.T) {
	client := NewGeminiClient("", "")

	messages := []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Go"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ToolCall: &ToolCall{ID: "tc-1", Name: "read_file"}},
			{Type: "tool_use", ToolCall: &ToolCall{ID: "tc-2", Name: "get_status"}},
		}},
	}

	if name := client.findFunctionName(messages, "tc-1"); name != "read_file" {
		t.Errorf("expected read_file, got %s", name)
	}
	if name := client.findFunctionName(messages, "tc-2"); name != "get_status" {
		t.Errorf("expected get_status, got %s", name)
	}
	if name := client.findFunctionName(messages, "nonexistent"); name != "unknown" {
		t.Errorf("expected unknown, got %s", name)
	}
	// Empty messages
	if name := client.findFunctionName(nil, "tc-1"); name != "unknown" {
		t.Errorf("expected unknown for nil messages, got %s", name)
	}
}

func TestGeminiClient_ToolResultConversion(t *testing.T) {
	// Verify tool_result messages are converted with function response
	var capturedReq geminiRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content:      geminiContent{Role: "model", Parts: []geminiPart{{Text: "done"}}},
				FinishReason: "STOP",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL

	messages := []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Go"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ToolCall: &ToolCall{ID: "tc-1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)}},
		}},
		{Role: "tool_result", ToolResults: []ToolResult{{ToolCallID: "tc-1", Content: "file data"}}},
	}

	_, err := client.ChatWithTools(context.Background(), "", messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// user + model(with functionCall) + user(with functionResponse) = 3 contents
	if len(capturedReq.Contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(capturedReq.Contents))
	}
	// Last content should have function response
	lastContent := capturedReq.Contents[2]
	if lastContent.Role != "user" {
		t.Errorf("expected user role for function response, got %s", lastContent.Role)
	}
	if len(lastContent.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(lastContent.Parts))
	}
	if lastContent.Parts[0].FunctionResponse == nil {
		t.Fatal("expected function response")
	}
	if lastContent.Parts[0].FunctionResponse.Name != "read_file" {
		t.Errorf("expected read_file, got %s", lastContent.Parts[0].FunctionResponse.Name)
	}
}

func TestGeminiClient_ToolNilSchema(t *testing.T) {
	var capturedReq geminiRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)
		resp := geminiResponse{
			Candidates: []geminiCandidate{{
				Content:      geminiContent{Role: "model", Parts: []geminiPart{{Text: "ok"}}},
				FinishReason: "STOP",
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewGeminiClient("key", "model")
	client.baseURL = server.URL

	tools := []ToolDef{{Name: "get_status", Description: "Get status", InputSchema: nil}}
	_, err := client.ChatWithTools(context.Background(), "", []ToolMessage{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Go"}}},
	}, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nil schema should get default object params
	if len(capturedReq.Tools) == 0 {
		t.Fatal("expected tools")
	}
	params := capturedReq.Tools[0].FunctionDeclarations[0].Parameters
	if params["type"] != "object" {
		t.Errorf("expected type=object for nil schema, got %v", params["type"])
	}
}

func TestNewGeminiClient_Defaults(t *testing.T) {
	c := NewGeminiClient("", "")
	if c.model != "gemini-2.5-flash" {
		t.Errorf("expected default model, got %s", c.model)
	}
}

func TestGeminiClient_InterfaceCompliance(t *testing.T) {
	var _ Client = (*GeminiClient)(nil)
	var _ ToolClient = (*GeminiClient)(nil)
}
