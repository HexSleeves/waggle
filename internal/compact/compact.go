package compact

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Message represents a conversation message in the Queen's context
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	TokenEst  int       `json:"token_est"`
}

// Context manages the Queen's conversation context with compaction.
// All methods are safe for concurrent use.
type Context struct {
	mu               sync.Mutex
	messages         []Message
	compacted        []string // Summaries of compacted segments
	maxTokens        int
	currentTokens    int
	compactThreshold float64 // e.g., 0.8 = compact at 80% capacity
}

func NewContext(maxTokens int) *Context {
	return &Context{
		messages:         make([]Message, 0, 128),
		compacted:        make([]string, 0),
		maxTokens:        maxTokens,
		compactThreshold: 0.75,
	}
}

// Add appends a message and triggers compaction if needed
func (c *Context) Add(role, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tokens := EstimateTokens(content)
	msg := Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
		TokenEst:  tokens,
	}
	c.messages = append(c.messages, msg)
	c.currentTokens += tokens
}

// NeedsCompaction returns true if context is approaching capacity
func (c *Context) NeedsCompaction() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return float64(c.currentTokens) > float64(c.maxTokens)*c.compactThreshold
}

// Compact compresses older messages into a summary, keeping recent ones.
// The summarizer function is injected — it could call an LLM or use a simple heuristic.
func (c *Context) Compact(summarizer func(messages []Message) (string, error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) < 4 {
		return nil
	}

	// Keep the last 25% of messages, compact the rest
	keepCount := max(len(c.messages)/4, 2)
	toCompact := c.messages[:len(c.messages)-keepCount]
	toKeep := c.messages[len(c.messages)-keepCount:]

	summary, err := summarizer(toCompact)
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}

	c.compacted = append(c.compacted, summary)
	c.messages = toKeep
	c.recalcTokens()

	return nil
}

// Build returns the full context as a message list, including compacted summaries
func (c *Context) Build() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []Message

	if len(c.compacted) > 0 {
		summary := strings.Join(c.compacted, "\n---\n")
		result = append(result, Message{
			Role:    "system",
			Content: fmt.Sprintf("[Context Summary from previous interactions]\n%s", summary),
		})
	}

	result = append(result, c.messages...)
	return result
}

// Len returns the number of active (non-compacted) messages
func (c *Context) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

// TokenCount returns the estimated token count
func (c *Context) TokenCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentTokens
}

func (c *Context) recalcTokens() {
	c.currentTokens = 0
	for _, s := range c.compacted {
		c.currentTokens += EstimateTokens(s)
	}
	for _, m := range c.messages {
		c.currentTokens += m.TokenEst
	}
}

// EstimateTokens gives a rough token count (~4 chars per token)
func EstimateTokens(s string) int {
	return len(s) / 4
}

// DefaultSummarizer provides a simple extractive summary without calling an LLM
func DefaultSummarizer(messages []Message) (string, error) {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Summary of %d messages:\n", len(messages)))

	for _, m := range messages {
		preview := m.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		b.WriteString(fmt.Sprintf("- [%s] %s\n", m.Role, preview))
	}
	return b.String(), nil
}

// LLMSummarizer returns a summarizer function that uses an LLM to create concise summaries.
// Falls back to DefaultSummarizer if the LLM call fails.
func LLMSummarizer(chatFn func(ctx context.Context, systemPrompt, message string) (string, error)) func(messages []Message) (string, error) {
	return func(messages []Message) (string, error) {
		// Build a prompt from the messages
		var b strings.Builder
		for _, m := range messages {
			preview := m.Content
			if len(preview) > 500 {
				preview = preview[:500] + "..."
			}
			b.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, preview))
		}

		summary, err := chatFn(context.Background(),
			"Summarize this conversation segment concisely. Focus on key decisions, tool calls, and outcomes. Keep under 500 words.",
			b.String())
		if err != nil {
			// Fallback to default
			return DefaultSummarizer(messages)
		}

		// Ensure summary isn't too long (cap at ~2000 tokens ≈ 8000 chars)
		if len(summary) > 8000 {
			summary = summary[:8000] + "\n[summary truncated]"
		}
		return summary, nil
	}
}
