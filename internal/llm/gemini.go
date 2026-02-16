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

// GeminiClient implements Client and ToolClient for Google's Gemini API.
type GeminiClient struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// Gemini API types

type geminiRequest struct {
	Contents          []geminiContent  `json:"contents"`
	Tools             []geminiTool     `json:"tools,omitempty"`
	SystemInstruction *geminiContent   `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResp `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFunctionResp struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type geminiGenConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	Error         *geminiError      `json:"error,omitempty"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
	ModelVersion  string            `json:"modelVersion,omitempty"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"` // "STOP", "MAX_TOKENS", "SAFETY", etc.
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// NewGeminiClient creates a client for Google's Gemini API.
// If apiKey is empty, it reads GEMINI_API_KEY (or GOOGLE_API_KEY) from the environment.
func NewGeminiClient(apiKey, model string) *GeminiClient {
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}

	return &GeminiClient{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://generativelanguage.googleapis.com/v1beta",
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *GeminiClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	return c.ChatWithHistory(ctx, systemPrompt, []Message{{Role: "user", Content: userMessage}})
}

func (c *GeminiClient) ChatWithHistory(ctx context.Context, systemPrompt string, messages []Message) (string, error) {
	req := geminiRequest{
		GenerationConfig: &geminiGenConfig{MaxOutputTokens: 4096},
	}

	if systemPrompt != "" {
		req.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		}
	}

	for _, m := range messages {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		req.Contents = append(req.Contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	resp, err := c.doRequest(ctx, req)
	if err != nil {
		return "", err
	}

	if len(resp.Candidates) == 0 {
		return "", fmt.Errorf("gemini: no candidates in response")
	}

	var out strings.Builder
	for _, p := range resp.Candidates[0].Content.Parts {
		if p.Text != "" {
			out.WriteString(p.Text)
		}
	}
	return out.String(), nil
}

func (c *GeminiClient) ChatWithTools(ctx context.Context, systemPrompt string,
	messages []ToolMessage, tools []ToolDef) (*Response, error) {

	req := geminiRequest{
		GenerationConfig: &geminiGenConfig{MaxOutputTokens: 8192},
	}

	if systemPrompt != "" {
		req.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		}
	}

	// Convert tool definitions
	if len(tools) > 0 {
		var decls []geminiFuncDecl
		for _, td := range tools {
			params := td.InputSchema
			if params == nil {
				params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}
			// Ensure top-level type is set
			if _, ok := params["type"]; !ok {
				params["type"] = "object"
			}
			decls = append(decls, geminiFuncDecl{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  params,
			})
		}
		req.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}

	// Convert messages
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			var parts []geminiPart
			for _, b := range msg.Content {
				if b.Type == "text" {
					parts = append(parts, geminiPart{Text: b.Text})
				}
			}
			if len(parts) > 0 {
				req.Contents = append(req.Contents, geminiContent{Role: "user", Parts: parts})
			}

		case "assistant":
			var parts []geminiPart
			for _, b := range msg.Content {
				if b.Type == "text" && b.Text != "" {
					parts = append(parts, geminiPart{Text: b.Text})
				}
				if b.Type == "tool_use" && b.ToolCall != nil {
					var args map[string]interface{}
					_ = json.Unmarshal(b.ToolCall.Input, &args)
					parts = append(parts, geminiPart{
						FunctionCall: &geminiFunctionCall{
							Name: b.ToolCall.Name,
							Args: args,
						},
					})
				}
			}
			if len(parts) > 0 {
				req.Contents = append(req.Contents, geminiContent{Role: "model", Parts: parts})
			}

		case "tool_result":
			var parts []geminiPart
			for _, tr := range msg.ToolResults {
				// Gemini needs the function name in the response. We store it
				// by looking up from the preceding assistant message's tool calls.
				funcName := c.findFunctionName(messages, tr.ToolCallID)
				resp := map[string]interface{}{"result": tr.Content}
				if tr.IsError {
					resp["error"] = tr.Content
				}
				parts = append(parts, geminiPart{
					FunctionResponse: &geminiFunctionResp{
						Name:     funcName,
						Response: resp,
					},
				})
			}
			if len(parts) > 0 {
				req.Contents = append(req.Contents, geminiContent{Role: "user", Parts: parts})
			}
		}
	}

	resp, err := c.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: no candidates in response")
	}

	candidate := resp.Candidates[0]

	// Map finish reason
	stopReason := "end_turn"
	// Check if any parts have function calls
	hasFuncCalls := false
	for _, p := range candidate.Content.Parts {
		if p.FunctionCall != nil {
			hasFuncCalls = true
			break
		}
	}
	if hasFuncCalls {
		stopReason = "tool_use"
	} else if candidate.FinishReason == "MAX_TOKENS" {
		stopReason = "max_tokens"
	}

	result := &Response{StopReason: stopReason}
	result.Model = resp.ModelVersion
	if resp.UsageMetadata != nil {
		result.Usage = Usage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
		}
	}

	// toolCallCounter generates stable IDs since Gemini doesn't provide them
	tcID := 0
	for _, p := range candidate.Content.Parts {
		if p.Text != "" {
			result.Content = append(result.Content, ContentBlock{Type: "text", Text: p.Text})
		}
		if p.FunctionCall != nil {
			argsJSON, _ := json.Marshal(p.FunctionCall.Args)
			result.Content = append(result.Content, ContentBlock{
				Type: "tool_use",
				ToolCall: &ToolCall{
					ID:    fmt.Sprintf("gemini-call-%d", tcID),
					Name:  p.FunctionCall.Name,
					Input: argsJSON,
				},
			})
			tcID++
		}
	}

	return result, nil
}

// findFunctionName looks through messages to find the function name for a tool call ID.
func (c *GeminiClient) findFunctionName(messages []ToolMessage, toolCallID string) string {
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, b := range msg.Content {
			if b.Type == "tool_use" && b.ToolCall != nil && b.ToolCall.ID == toolCallID {
				return b.ToolCall.Name
			}
		}
	}
	return "unknown"
}

func (c *GeminiClient) doRequest(ctx context.Context, body geminiRequest) (*geminiResponse, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", c.baseURL, c.model)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	httpResp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("gemini: read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini: API error %d: %s", httpResp.StatusCode, string(respBody))
	}

	var resp geminiResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("gemini: unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("gemini: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}

	return &resp, nil
}
