package llm

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// AnthropicClient wraps the Anthropic SDK.
type AnthropicClient struct {
	client *anthropic.Client
	model  string
}

func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	c := anthropic.NewClient(opts...)
	return &AnthropicClient{
		client: &c,
		model:  model,
	}
}

func (c *AnthropicClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	return c.ChatWithHistory(ctx, systemPrompt, []Message{{Role: "user", Content: userMessage}})
}

func (c *AnthropicClient) ChatWithHistory(ctx context.Context, systemPrompt string, messages []Message) (string, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: 4096,
		Messages:  toAnthropicMessages(messages),
	}
	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Text: systemPrompt}}
	}

	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String(), nil
}

func toAnthropicMessages(msgs []Message) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, len(msgs))
	for i, m := range msgs {
		if m.Role == "assistant" {
			out[i] = anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content))
		} else {
			out[i] = anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content))
		}
	}
	return out
}

// ChatWithTools sends a message with tool definitions and returns the response,
// which may include tool-use requests.
func (c *AnthropicClient) ChatWithTools(ctx context.Context, systemPrompt string,
	messages []ToolMessage, tools []ToolDef) (*Response, error) {

	// Convert tool definitions to Anthropic SDK params.
	apiTools := make([]anthropic.ToolUnionParam, len(tools))
	for i, td := range tools {
		props, _ := td.InputSchema["properties"].(map[string]interface{})
		schema := anthropic.ToolInputSchemaParam{
			Properties: props,
		}
		if req, ok := td.InputSchema["required"].([]interface{}); ok {
			reqStrings := make([]string, len(req))
			for j, r := range req {
				reqStrings[j], _ = r.(string)
			}
			schema.Required = reqStrings
		}
		t := anthropic.ToolUnionParamOfTool(schema, td.Name)
		if td.Description != "" {
			t.OfTool.Description = param.NewOpt(td.Description)
		}
		if td.Cache {
			t.OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		apiTools[i] = t
	}

	// Convert ToolMessages to Anthropic MessageParams.
	apiMessages := make([]anthropic.MessageParam, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
			for _, b := range msg.Content {
				if b.Type == "text" {
					blocks = append(blocks, anthropic.NewTextBlock(b.Text))
				}
			}
			apiMessages = append(apiMessages, anthropic.NewUserMessage(blocks...))

		case "assistant":
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
			for _, b := range msg.Content {
				switch b.Type {
				case "text":
					blocks = append(blocks, anthropic.NewTextBlock(b.Text))
				case "tool_use":
					if b.ToolCall != nil {
						var inputMap map[string]interface{}
						_ = json.Unmarshal(b.ToolCall.Input, &inputMap)
						blocks = append(blocks, anthropic.NewToolUseBlock(b.ToolCall.ID, inputMap, b.ToolCall.Name))
					}
				}
			}
			apiMessages = append(apiMessages, anthropic.NewAssistantMessage(blocks...))

		case "tool_result":
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.ToolResults))
			for _, tr := range msg.ToolResults {
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, tr.Content, tr.IsError))
			}
			apiMessages = append(apiMessages, anthropic.NewUserMessage(blocks...))
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: 8192,
		Messages:  apiMessages,
		Tools:     apiTools,
	}
	if systemPrompt != "" {
		sysBlocks := []anthropic.TextBlockParam{{Text: systemPrompt}}
		sysBlocks[len(sysBlocks)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
		params.System = sysBlocks
	}

	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	// Parse response into our Response type.
	result := &Response{
		StopReason: string(resp.StopReason),
		Model:      string(resp.Model),
		Usage: Usage{
			InputTokens:         int(resp.Usage.InputTokens),
			OutputTokens:        int(resp.Usage.OutputTokens),
			CacheCreationTokens: int(resp.Usage.CacheCreationInputTokens),
			CacheReadTokens:     int(resp.Usage.CacheReadInputTokens),
		},
	}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Content = append(result.Content, ContentBlock{
				Type: "text",
				Text: block.Text,
			})
		case "tool_use":
			toolUse := block.AsToolUse()
			result.Content = append(result.Content, ContentBlock{
				Type: "tool_use",
				ToolCall: &ToolCall{
					ID:    toolUse.ID,
					Name:  toolUse.Name,
					Input: toolUse.Input,
				},
			})
		}
	}

	return result, nil
}
