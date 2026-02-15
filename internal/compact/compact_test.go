package compact

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewContext(t *testing.T) {
	c := NewContext(1000)
	if c.Len() != 0 {
		t.Errorf("NewContext Len() = %d, want 0", c.Len())
	}
	if c.TokenCount() != 0 {
		t.Errorf("NewContext TokenCount() = %d, want 0", c.TokenCount())
	}
}

func TestAdd(t *testing.T) {
	c := NewContext(10000)

	c.Add("user", "hello world")
	if c.Len() != 1 {
		t.Errorf("after 1 Add, Len() = %d, want 1", c.Len())
	}
	if c.TokenCount() == 0 {
		t.Error("after Add, TokenCount() should be > 0")
	}

	tokensBefore := c.TokenCount()
	c.Add("assistant", "hi there, how can I help you today?")
	if c.Len() != 2 {
		t.Errorf("after 2 Adds, Len() = %d, want 2", c.Len())
	}
	if c.TokenCount() <= tokensBefore {
		t.Errorf("TokenCount() should increase after Add: got %d, was %d", c.TokenCount(), tokensBefore)
	}
}

func TestNeedsCompaction(t *testing.T) {
	// maxTokens=100, threshold=0.75 → triggers at 75 tokens
	// EstimateTokens uses len/4, so 75 tokens = 300 chars
	c := NewContext(100)

	// Add a small message — well below threshold
	c.Add("user", "short")
	if c.NeedsCompaction() {
		t.Error("NeedsCompaction() should be false when well below threshold")
	}

	// Add enough content to exceed 75% of 100 tokens = 75 tokens → 300+ chars
	c.Add("user", strings.Repeat("x", 320))
	if !c.NeedsCompaction() {
		t.Errorf("NeedsCompaction() should be true when above 75%% threshold; TokenCount()=%d, maxTokens=100", c.TokenCount())
	}
}

func TestCompactReducesMessages(t *testing.T) {
	c := NewContext(100000)

	// Add 8 messages so compaction has something to work with
	for i := 0; i < 8; i++ {
		c.Add("user", fmt.Sprintf("message number %d with some content", i))
	}
	if c.Len() != 8 {
		t.Fatalf("before compaction Len() = %d, want 8", c.Len())
	}

	err := c.Compact(DefaultSummarizer)
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}

	// keepCount = max(8/4, 2) = 2, so 6 compacted, 2 kept
	if c.Len() != 2 {
		t.Errorf("after compaction Len() = %d, want 2", c.Len())
	}

	// Build should contain the summary as the first message
	msgs := c.Build()
	if len(msgs) < 1 {
		t.Fatal("Build() returned empty after compaction")
	}
	if msgs[0].Role != "system" {
		t.Errorf("first message role = %q, want %q", msgs[0].Role, "system")
	}
	if !strings.Contains(msgs[0].Content, "Context Summary") {
		t.Errorf("summary message should contain 'Context Summary', got: %s", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "Summary of 6 messages") {
		t.Errorf("summary should mention 6 messages, got: %s", msgs[0].Content)
	}
}

func TestCompactFewMessages(t *testing.T) {
	c := NewContext(10000)

	c.Add("user", "one")
	c.Add("assistant", "two")
	c.Add("user", "three")

	lenBefore := c.Len()
	err := c.Compact(DefaultSummarizer)
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if c.Len() != lenBefore {
		t.Errorf("Compact with < 4 messages should be no-op: Len() changed from %d to %d", lenBefore, c.Len())
	}

	// Build should NOT contain a summary system message
	msgs := c.Build()
	for _, m := range msgs {
		if strings.Contains(m.Content, "Context Summary") {
			t.Error("no summary should exist after no-op compaction")
		}
	}
}

func TestCompactFailingSummarizer(t *testing.T) {
	c := NewContext(10000)
	for i := 0; i < 8; i++ {
		c.Add("user", fmt.Sprintf("message %d", i))
	}

	lenBefore := c.Len()
	failSummarizer := func(messages []Message) (string, error) {
		return "", errors.New("summarizer exploded")
	}

	err := c.Compact(failSummarizer)
	if err == nil {
		t.Fatal("Compact should return error when summarizer fails")
	}
	if !strings.Contains(err.Error(), "summarizer exploded") {
		t.Errorf("error should wrap original: got %v", err)
	}
	if !strings.Contains(err.Error(), "summarize:") {
		t.Errorf("error should have 'summarize:' prefix: got %v", err)
	}
	// Messages should be unchanged on error
	if c.Len() != lenBefore {
		t.Errorf("Len() should be unchanged after failed compaction: got %d, want %d", c.Len(), lenBefore)
	}
}

func TestBuildWithoutCompaction(t *testing.T) {
	c := NewContext(10000)
	c.Add("user", "first")
	c.Add("assistant", "second")
	c.Add("user", "third")

	msgs := c.Build()
	if len(msgs) != 3 {
		t.Fatalf("Build() returned %d messages, want 3", len(msgs))
	}

	// Verify order is preserved
	expected := []struct {
		role, content string
	}{
		{"user", "first"},
		{"assistant", "second"},
		{"user", "third"},
	}
	for i, exp := range expected {
		if msgs[i].Role != exp.role {
			t.Errorf("msgs[%d].Role = %q, want %q", i, msgs[i].Role, exp.role)
		}
		if msgs[i].Content != exp.content {
			t.Errorf("msgs[%d].Content = %q, want %q", i, msgs[i].Content, exp.content)
		}
	}
}

func TestBuildWithCompaction(t *testing.T) {
	c := NewContext(100000)
	for i := 0; i < 12; i++ {
		c.Add("user", fmt.Sprintf("msg-%d", i))
	}

	err := c.Compact(DefaultSummarizer)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	msgs := c.Build()

	// First message should be the system summary
	if msgs[0].Role != "system" {
		t.Errorf("first message should be system summary, got role=%q", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "Context Summary") {
		t.Error("first message should contain context summary")
	}

	// Remaining messages should be the recent ones
	// keepCount = max(12/4, 2) = 3
	if len(msgs) != 4 { // 1 summary + 3 kept
		t.Errorf("Build() returned %d messages, want 4 (1 summary + 3 recent)", len(msgs))
	}

	// Last message should be the most recent
	last := msgs[len(msgs)-1]
	if last.Content != "msg-11" {
		t.Errorf("last message content = %q, want %q", last.Content, "msg-11")
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"abcd", 1},
		{"abcdefgh", 2},
		{"ab", 0},       // 2/4 = 0 (integer division)
		{"abcde", 1},   // 5/4 = 1
		{strings.Repeat("a", 400), 100},
	}
	for _, tt := range tests {
		got := EstimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestDefaultSummarizer(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello there"},
		{Role: "assistant", Content: "Hi! How can I help?"},
		{Role: "user", Content: "Tell me about Go"},
	}

	summary, err := DefaultSummarizer(messages)
	if err != nil {
		t.Fatalf("DefaultSummarizer returned error: %v", err)
	}

	// Should mention message count
	if !strings.Contains(summary, "Summary of 3 messages") {
		t.Errorf("summary should contain message count, got: %s", summary)
	}

	// Should contain content previews
	if !strings.Contains(summary, "Hello there") {
		t.Error("summary should contain content preview 'Hello there'")
	}
	if !strings.Contains(summary, "[user]") {
		t.Error("summary should contain role labels")
	}
	if !strings.Contains(summary, "[assistant]") {
		t.Error("summary should contain assistant role label")
	}
}

func TestDefaultSummarizerTruncatesLongContent(t *testing.T) {
	longContent := strings.Repeat("x", 500)
	messages := []Message{
		{Role: "user", Content: longContent},
	}

	summary, err := DefaultSummarizer(messages)
	if err != nil {
		t.Fatalf("DefaultSummarizer returned error: %v", err)
	}

	// The preview should be truncated to 200 chars + "..."
	if !strings.Contains(summary, "...") {
		t.Error("long content should be truncated with '...'")
	}

	// The full 500-char string should NOT appear
	if strings.Contains(summary, longContent) {
		t.Error("summary should not contain the full 500-char string")
	}

	// The truncated preview (200 x's) should appear
	truncated := strings.Repeat("x", 200) + "..."
	if !strings.Contains(summary, truncated) {
		t.Error("summary should contain exactly 200-char truncated preview")
	}
}
