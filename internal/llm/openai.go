package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// OpenAIClient implements Client and ToolClient for OpenAI-compatible APIs.
// Works with OpenAI, Codex, Azure OpenAI, and any compatible endpoint.
type OpenAIClient struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// OpenAI API request/response types

type openaiRequest struct {
	Model               string          `json:"model"`
	Messages            []openaiMessage `json:"messages"`
	Tools               []openaiTool    `json:"tools,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"` // string or []openaiContentPart
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openaiTool struct {
	Type     string         `json:"type"` // "function"
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function openaiCallFunction `json:"function"`
}

type openaiCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Error   *openaiError   `json:"error,omitempty"`
	Usage   *openaiUsage   `json:"usage,omitempty"`
	Model   string         `json:"model,omitempty"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiChoice struct {
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"` // "stop", "tool_calls", "length"
}

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// NewOpenAIClient creates a client for OpenAI-compatible APIs.
// If apiKey is empty, it reads OPENAI_API_KEY from the environment.
// If baseURL is empty, it defaults to https://api.openai.com/v1.
func NewOpenAIClient(apiKey, model, baseURL string) *OpenAIClient {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if model == "" {
		model = "gpt-4o"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	// Trim trailing slash
	baseURL = strings.TrimRight(baseURL, "/")

	return &OpenAIClient{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *OpenAIClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	return c.ChatWithHistory(ctx, systemPrompt, []Message{{Role: "user", Content: userMessage}})
}

func (c *OpenAIClient) ChatWithHistory(ctx context.Context, systemPrompt string, messages []Message) (string, error) {
	var apiMessages []openaiMessage
	if systemPrompt != "" {
		apiMessages = append(apiMessages, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		apiMessages = append(apiMessages, openaiMessage{Role: m.Role, Content: m.Content})
	}

	reqBody := openaiRequest{
		Model:               c.model,
		Messages:            apiMessages,
		MaxCompletionTokens: 4096,
	}

	resp, err := c.doRequest(ctx, reqBody)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices in response")
	}

	// Extract text content
	content := resp.Choices[0].Message.Content
	switch v := content.(type) {
	case string:
		return v, nil
	default:
		raw, _ := json.Marshal(v)
		return string(raw), nil
	}
}

func (c *OpenAIClient) ChatWithTools(ctx context.Context, systemPrompt string,
	messages []ToolMessage, tools []ToolDef) (*Response, error) {

	// Build API messages
	var apiMessages []openaiMessage
	if systemPrompt != "" {
		apiMessages = append(apiMessages, openaiMessage{Role: "system", Content: systemPrompt})
	}

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			var text string
			for _, b := range msg.Content {
				if b.Type == "text" {
					text += b.Text
				}
			}
			apiMessages = append(apiMessages, openaiMessage{Role: "user", Content: text})

		case "assistant":
			amsg := openaiMessage{Role: "assistant"}
			var textParts []string
			for _, b := range msg.Content {
				if b.Type == "text" && b.Text != "" {
					textParts = append(textParts, b.Text)
				}
				if b.Type == "tool_use" && b.ToolCall != nil {
					amsg.ToolCalls = append(amsg.ToolCalls, openaiToolCall{
						ID:   b.ToolCall.ID,
						Type: "function",
						Function: openaiCallFunction{
							Name:      b.ToolCall.Name,
							Arguments: string(b.ToolCall.Input),
						},
					})
				}
			}
			if len(textParts) > 0 {
				amsg.Content = strings.Join(textParts, "\n")
			}
			apiMessages = append(apiMessages, amsg)

		case "tool_result":
			// OpenAI uses separate messages per tool result with role="tool"
			for _, tr := range msg.ToolResults {
				apiMessages = append(apiMessages, openaiMessage{
					Role:       "tool",
					Content:    tr.Content,
					ToolCallID: tr.ToolCallID,
				})
			}
		}
	}

	// Build tool definitions
	var apiTools []openaiTool
	for _, td := range tools {
		apiTools = append(apiTools, openaiTool{
			Type: "function",
			Function: openaiFunction{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.InputSchema,
			},
		})
	}

	reqBody := openaiRequest{
		Model:               c.model,
		Messages:            apiMessages,
		Tools:               apiTools,
		MaxCompletionTokens: 8192,
	}

	resp, err := c.doRequest(ctx, reqBody)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices in response")
	}

	choice := resp.Choices[0]

	// Map finish_reason to our StopReason
	stopReason := "end_turn"
	switch choice.FinishReason {
	case "tool_calls":
		stopReason = "tool_use"
	case "length":
		stopReason = "max_tokens"
	case "stop":
		stopReason = "end_turn"
	}

	result := &Response{StopReason: stopReason}
	result.Model = resp.Model
	if resp.Usage != nil {
		result.Usage = Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
	}

	// Extract text content
	if choice.Message.Content != nil {
		switch v := choice.Message.Content.(type) {
		case string:
			if v != "" {
				result.Content = append(result.Content, ContentBlock{Type: "text", Text: v})
			}
		}
	}

	// Extract tool calls
	for _, tc := range choice.Message.ToolCalls {
		result.Content = append(result.Content, ContentBlock{
			Type: "tool_use",
			ToolCall: &ToolCall{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			},
		})
	}

	return result, nil
}

func (c *OpenAIClient) doRequest(ctx context.Context, body openaiRequest) (*openaiResponse, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	httpResp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: API error %d: %s", httpResp.StatusCode, string(respBody))
	}

	var resp openaiResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("openai: unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("openai: %s: %s", resp.Error.Type, resp.Error.Message)
	}

	return &resp, nil
}
