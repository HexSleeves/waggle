package compact

import (
	"fmt"
	"strings"
	"time"
)

// Message represents a conversation message in the Queen's context
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	TokenEst  int       `json:"token_est"`
}

// Context manages the Queen's conversation context with compaction
type Context struct {
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
	return float64(c.currentTokens) > float64(c.maxTokens)*c.compactThreshold
}

// Compact compresses older messages into a summary, keeping recent ones.
// The summarizer function is injected — it could call an LLM or use a simple heuristic.
func (c *Context) Compact(summarizer func(messages []Message) (string, error)) error {
	if len(c.messages) < 4 {
		return nil
	}

	// Keep the last 25% of messages, compact the rest
	keepCount := len(c.messages) / 4
	if keepCount < 2 {
		keepCount = 2
	}
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
	return len(c.messages)
}

// TokenCount returns the estimated token count
func (c *Context) TokenCount() int {
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
