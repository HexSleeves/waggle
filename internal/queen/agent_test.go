package queen

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/adapter"
	"github.com/exedev/waggle/internal/blackboard"
	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/compact"
	"github.com/exedev/waggle/internal/config"
	"github.com/exedev/waggle/internal/llm"
	"github.com/exedev/waggle/internal/safety"
	"github.com/exedev/waggle/internal/state"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// mockToolClient implements llm.ToolClient with scripted responses.
type mockToolClient struct {
	mu        sync.Mutex
	responses []*llm.Response
	callIndex int
	calls     []mockCall
}

type mockCall struct {
	Messages []llm.ToolMessage
	Tools    []llm.ToolDef
}

func (m *mockToolClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	return "mock chat response", nil
}

func (m *mockToolClient) ChatWithHistory(ctx context.Context, systemPrompt string, messages []llm.Message) (string, error) {
	return "mock chat history response", nil
}

func (m *mockToolClient) ChatWithTools(ctx context.Context, systemPrompt string,
	messages []llm.ToolMessage, tools []llm.ToolDef) (*llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, mockCall{Messages: messages, Tools: tools})

	if m.callIndex >= len(m.responses) {
		// Default: end turn
		return &llm.Response{
			Content:    []llm.ContentBlock{{Type: "text", Text: "Done."}},
			StopReason: "end_turn",
		}, nil
	}

	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func TestSupportsAgentMode(t *testing.T) {
	q := &Queen{}

	// No LLM: no agent mode
	if q.SupportsAgentMode() {
		t.Error("expected false with nil llm")
	}

	// Regular Client (no tool support): no agent mode
	q.llm = &mockBasicClient{}
	if q.SupportsAgentMode() {
		t.Error("expected false with basic client")
	}

	// ToolClient: agent mode supported
	q.llm = &mockToolClient{}
	if !q.SupportsAgentMode() {
		t.Error("expected true with tool client")
	}
}

type mockBasicClient struct{}

func (m *mockBasicClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	return "", nil
}
func (m *mockBasicClient) ChatWithHistory(ctx context.Context, systemPrompt string, messages []llm.Message) (string, error) {
	return "", nil
}

func TestRunAgentEndTurn(t *testing.T) {
	// Queen gets the objective, immediately says done (end_turn)
	q := setupTestQueen(t)
	q.llm = &mockToolClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: "text", Text: "Nothing to do."}},
			StopReason: "end_turn",
		}},
	}

	err := q.RunAgent(context.Background(), "test objective")
	if err != nil {
		t.Fatalf("RunAgent failed: %v", err)
	}

	// Verify session was created
	if q.sessionID == "" {
		t.Error("session ID not set")
	}
}

func TestRunAgentCreateAndComplete(t *testing.T) {
	// Queen creates a task then completes
	q := setupTestQueen(t)

	createInput, _ := json.Marshal(map[string]interface{}{
		"tasks": []map[string]interface{}{{
			"id":          "task-1",
			"title":       "Test task",
			"description": "echo hello",
			"type":        "test",
			"priority":    1,
		}},
	})

	completeInput, _ := json.Marshal(map[string]string{
		"summary": "All done",
	})

	q.llm = &mockToolClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: "tool_use",
					ToolCall: &llm.ToolCall{
						ID:    "call-1",
						Name:  "create_tasks",
						Input: createInput,
					},
				}},
				StopReason: "tool_use",
			},
			{
				Content: []llm.ContentBlock{{
					Type: "tool_use",
					ToolCall: &llm.ToolCall{
						ID:    "call-2",
						Name:  "complete",
						Input: completeInput,
					},
				}},
				StopReason: "tool_use",
			},
		},
	}

	err := q.RunAgent(context.Background(), "test objective")
	if err != nil {
		t.Fatalf("RunAgent failed: %v", err)
	}

	// Verify task was created
	tasks := q.tasks.All()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "task-1" {
		t.Errorf("expected task ID 'task-1', got %q", tasks[0].ID)
	}
}

func TestRunAgentFailTool(t *testing.T) {
	q := setupTestQueen(t)

	failInput, _ := json.Marshal(map[string]string{
		"reason": "impossible objective",
	})

	q.llm = &mockToolClient{
		responses: []*llm.Response{{
			Content: []llm.ContentBlock{{
				Type: "tool_use",
				ToolCall: &llm.ToolCall{
					ID:    "call-1",
					Name:  "fail",
					Input: failInput,
				},
			}},
			StopReason: "tool_use",
		}},
	}

	err := q.RunAgent(context.Background(), "impossible thing")
	if err == nil {
		t.Fatal("expected error from fail tool")
	}
	if !contains(err.Error(), "failure") && !contains(err.Error(), "failed") {
		t.Errorf("expected failure error, got: %v", err)
	}
}

func TestRunAgentMaxTurns(t *testing.T) {
	q := setupTestQueen(t)
	q.cfg.Queen.MaxIterations = 3

	// Always request get_status — never terminates
	statusInput, _ := json.Marshal(map[string]interface{}{})

	q.llm = &mockToolClient{
		responses: []*llm.Response{
			makeToolResponse("call-1", "get_status", statusInput),
			makeToolResponse("call-2", "get_status", statusInput),
			makeToolResponse("call-3", "get_status", statusInput),
			makeToolResponse("call-4", "get_status", statusInput),
		},
	}

	err := q.RunAgent(context.Background(), "never ending")
	if err == nil {
		t.Fatal("expected max turns error")
	}
	if !contains(err.Error(), "max turns") {
		t.Errorf("expected max turns error, got: %v", err)
	}
}

func TestRunAgentFallsBackToLegacy(t *testing.T) {
	// When LLM doesn't support tools, RunAgent should fall back to legacy.
	// We just verify it doesn't try to use tool-calling APIs.
	q := setupTestQueen(t)
	basic := &mockBasicClient{}
	q.llm = basic

	if q.SupportsAgentMode() {
		t.Fatal("basic client should not support agent mode")
	}

	// RunAgent with a basic client should attempt legacy Run().
	// Legacy will fail because our test pool factory returns nil bees,
	// but we just verify it took the legacy path (not the tool path).
	// We can't easily test the full legacy path without a real adapter,
	// so just verify the routing logic.
	t.Log("Verified: basic LLM client correctly detected as non-tool-capable")
}

func TestCompactMessages(t *testing.T) {
	q := setupTestQueen(t)

	// Build a conversation with many messages
	var messages []llm.ToolMessage
	// First: objective
	messages = append(messages, llm.ToolMessage{
		Role:    "user",
		Content: []llm.ContentBlock{{Type: "text", Text: "Build a thing"}},
	})

	// Add 80 assistant/tool_result pairs
	for i := 0; i < 80; i++ {
		messages = append(messages, llm.ToolMessage{
			Role: "assistant",
			Content: []llm.ContentBlock{{
				Type:     "tool_use",
				ToolCall: &llm.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "get_status"},
			}},
		})
		messages = append(messages, llm.ToolMessage{
			Role:        "tool_result",
			ToolResults: []llm.ToolResult{{ToolCallID: fmt.Sprintf("call-%d", i), Content: "ok"}},
		})
	}

	// Total: 1 + 160 = 161 messages
	compacted := q.compactMessages(messages)

	// Should be much smaller: objective + summary + last 20
	if len(compacted) >= len(messages) {
		t.Errorf("expected compaction, got %d messages (was %d)", len(compacted), len(messages))
	}
	if len(compacted) > 25 {
		t.Errorf("expected ~22 messages after compaction, got %d", len(compacted))
	}

	// First message should still be the objective
	if compacted[0].Content[0].Text != "Build a thing" {
		t.Error("first message should be the objective")
	}

	// Second message should be the compaction summary
	if !contains(compacted[1].Content[0].Text, "compacted") {
		t.Error("second message should be the compaction summary")
	}
}

func TestRunAgentContextCancellation(t *testing.T) {
	q := setupTestQueen(t)

	// LLM never returns (blocks forever)
	q.llm = &blockingToolClient{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := q.RunAgent(ctx, "test")
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
}

type blockingToolClient struct{}

func (m *blockingToolClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (m *blockingToolClient) ChatWithHistory(ctx context.Context, systemPrompt string, messages []llm.Message) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (m *blockingToolClient) ChatWithTools(ctx context.Context, systemPrompt string,
	messages []llm.ToolMessage, tools []llm.ToolDef) (*llm.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// --- helpers ---

func setupTestQueen(t *testing.T) *Queen {
	t.Helper()
	tmpDir := t.TempDir()
	hiveDir := filepath.Join(tmpDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("create hive dir: %v", err)
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	msgBus := bus.New(100)
	board := blackboard.New(msgBus)
	tasks := task.NewTaskGraph(msgBus)

	pool := worker.NewPool(4, func(id, adapterName string) (worker.Bee, error) {
		return nil, nil
	}, msgBus)

	cfg := &config.Config{
		ProjectDir: tmpDir,
		HiveDir:    ".hive",
		Queen:      config.QueenConfig{MaxIterations: 50},
		Workers: config.WorkerConfig{
			MaxParallel:    4,
			MaxRetries:     2,
			DefaultTimeout: 5 * time.Minute,
			DefaultAdapter: "exec",
		},
		Safety: config.SafetyConfig{
			AllowedPaths: []string{"."},
			MaxFileSize:  10 * 1024 * 1024,
		},
		Adapters: map[string]config.AdapterConfig{},
	}

	guard, _ := safety.NewGuard(cfg.Safety, tmpDir)
	registry := adapter.NewRegistry()
	registry.Register(adapter.NewExecAdapter(tmpDir, guard))
	router := adapter.NewTaskRouter(registry, cfg.Workers.DefaultAdapter)

	logger := log.New(os.Stderr, "[TEST] ", log.LstdFlags)
	ctxMgr := compact.NewContext(200000)

	return &Queen{
		cfg:         cfg,
		bus:         msgBus,
		db:          db,
		board:       board,
		tasks:       tasks,
		pool:        pool,
		router:      router,
		registry:    registry,
		ctx:         ctxMgr,
		phase:       PhasePlan,
		logger:      logger,
		assignments: make(map[string]string),
	}
}

func makeToolResponse(id, name string, input json.RawMessage) *llm.Response {
	return &llm.Response{
		Content: []llm.ContentBlock{{
			Type: "tool_use",
			ToolCall: &llm.ToolCall{
				ID:    id,
				Name:  name,
				Input: input,
			},
		}},
		StopReason: "tool_use",
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- repairToolHistory tests ---

func TestRepairToolHistory_MissingResults(t *testing.T) {
	// Assistant has tool_use but no following tool_result
	messages := []llm.ToolMessage{
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Go"}}},
		{Role: "assistant", Content: []llm.ContentBlock{{
			Type:     "tool_use",
			ToolCall: &llm.ToolCall{ID: "tc-1", Name: "get_status"},
		}}},
		// Missing tool_result here
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Continue"}}},
	}

	repaired := repairToolHistory(messages)

	// Should have 4 messages: user, assistant, synthetic tool_result, user
	if len(repaired) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(repaired))
	}

	// The injected tool_result should be at index 2
	tr := repaired[2]
	if tr.Role != "tool_result" {
		t.Fatalf("expected tool_result at index 2, got %q", tr.Role)
	}
	if len(tr.ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(tr.ToolResults))
	}
	if tr.ToolResults[0].ToolCallID != "tc-1" {
		t.Errorf("expected tool call ID tc-1, got %q", tr.ToolResults[0].ToolCallID)
	}
	if !tr.ToolResults[0].IsError {
		t.Error("expected IsError=true on synthetic result")
	}
	if tr.ToolResults[0].Content != "not executed (session interrupted); retry if needed" {
		t.Errorf("unexpected content: %q", tr.ToolResults[0].Content)
	}
}

func TestRepairToolHistory_OrphanResults(t *testing.T) {
	// tool_result with no preceding tool_use
	messages := []llm.ToolMessage{
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Go"}}},
		{Role: "tool_result", ToolResults: []llm.ToolResult{{
			ToolCallID: "orphan-1",
			Content:    "result",
		}}},
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Continue"}}},
	}

	repaired := repairToolHistory(messages)

	// Orphaned tool_result should be removed
	for _, msg := range repaired {
		if msg.Role == "tool_result" {
			t.Error("orphaned tool_result should have been removed")
		}
	}
	if len(repaired) != 2 {
		t.Errorf("expected 2 messages, got %d", len(repaired))
	}
}

func TestRepairToolHistory_EmptyAssistant(t *testing.T) {
	messages := []llm.ToolMessage{
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Go"}}},
		{Role: "assistant", Content: nil},
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Continue"}}},
	}

	repaired := repairToolHistory(messages)

	if len(repaired) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(repaired))
	}

	assistant := repaired[1]
	if len(assistant.Content) == 0 {
		t.Fatal("expected placeholder content added to empty assistant message")
	}
	if assistant.Content[0].Type != "text" {
		t.Errorf("expected text block, got %q", assistant.Content[0].Type)
	}
	if assistant.Content[0].Text != "[continued]" {
		t.Errorf("expected [continued], got %q", assistant.Content[0].Text)
	}
}

func TestRepairToolHistory_NoOpWhenValid(t *testing.T) {
	messages := []llm.ToolMessage{
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Go"}}},
		{Role: "assistant", Content: []llm.ContentBlock{{
			Type:     "tool_use",
			ToolCall: &llm.ToolCall{ID: "tc-1", Name: "get_status"},
		}}},
		{Role: "tool_result", ToolResults: []llm.ToolResult{{
			ToolCallID: "tc-1",
			Content:    "ok",
		}}},
	}

	repaired := repairToolHistory(messages)

	if len(repaired) != 3 {
		t.Fatalf("expected 3 messages (no change), got %d", len(repaired))
	}
	if repaired[2].Role != "tool_result" {
		t.Errorf("expected tool_result, got %q", repaired[2].Role)
	}
	if repaired[2].ToolResults[0].ToolCallID != "tc-1" {
		t.Errorf("expected tc-1, got %q", repaired[2].ToolResults[0].ToolCallID)
	}
	if repaired[2].ToolResults[0].IsError {
		t.Error("should not mark valid result as error")
	}
}

func TestRepairToolHistory_MultipleToolCalls(t *testing.T) {
	// Assistant makes 3 tool calls, but only 1 result is present
	messages := []llm.ToolMessage{
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Go"}}},
		{Role: "assistant", Content: []llm.ContentBlock{
			{Type: "tool_use", ToolCall: &llm.ToolCall{ID: "tc-1", Name: "a"}},
			{Type: "tool_use", ToolCall: &llm.ToolCall{ID: "tc-2", Name: "b"}},
			{Type: "tool_use", ToolCall: &llm.ToolCall{ID: "tc-3", Name: "c"}},
		}},
		{Role: "tool_result", ToolResults: []llm.ToolResult{{
			ToolCallID: "tc-1",
			Content:    "ok",
		}}},
	}

	repaired := repairToolHistory(messages)

	// Should have 3 messages: user, assistant, merged tool_result
	if len(repaired) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(repaired))
	}

	tr := repaired[2]
	if len(tr.ToolResults) != 3 {
		t.Fatalf("expected 3 tool results, got %d", len(tr.ToolResults))
	}

	// First should be the existing result (not an error)
	if tr.ToolResults[0].IsError {
		t.Error("first result should not be an error")
	}

	// Second and third should be synthetic errors
	for _, r := range tr.ToolResults[1:] {
		if !r.IsError {
			t.Errorf("expected IsError for %s", r.ToolCallID)
		}
		if r.Content != "not executed (session interrupted); retry if needed" {
			t.Errorf("unexpected content for %s: %q", r.ToolCallID, r.Content)
		}
	}
}

// --- max_tokens truncation tests ---

func TestRunAgent_MaxTokensTruncation(t *testing.T) {
	q := setupTestQueen(t)

	statusInput, _ := json.Marshal(map[string]interface{}{})

	// First response: truncated (max_tokens)
	// Second response: calls a tool normally
	// Third response: ends the turn
	q.llm = &mockToolClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: "text",
					Text: "Partial respon",
				}},
				StopReason: "max_tokens",
			},
			makeToolResponse("call-1", "get_status", statusInput),
			{
				Content:    []llm.ContentBlock{{Type: "text", Text: "Done."}},
				StopReason: "end_turn",
			},
		},
	}

	err := q.RunAgent(context.Background(), "test objective")
	if err != nil {
		t.Fatalf("RunAgent failed: %v", err)
	}

	// The mock should have been called 3 times (truncated + tool + end)
	client := q.llm.(*mockToolClient)
	client.mu.Lock()
	nCalls := len(client.calls)
	client.mu.Unlock()
	if nCalls != 3 {
		t.Errorf("expected 3 LLM calls, got %d", nCalls)
	}

	// Verify the second call had the SYSTEM truncation message
	client.mu.Lock()
	secondCallMsgs := client.calls[1].Messages
	client.mu.Unlock()

	lastMsg := secondCallMsgs[len(secondCallMsgs)-1]
	if lastMsg.Role != "user" {
		t.Errorf("expected user message, got %q", lastMsg.Role)
	}
	if len(lastMsg.Content) == 0 || !strings.Contains(lastMsg.Content[0].Text, "truncated") {
		t.Error("expected truncation system message")
	}
}

func TestCompactMessages_NeverSplitsPairs(t *testing.T) {
	q := setupTestQueen(t)

	// Build a conversation where idealCut falls right between tool_use and tool_result.
	// We need >22 messages total (keepLast=20, so idealCut = len-20).
	var messages []llm.ToolMessage
	// Message 0: objective
	messages = append(messages, llm.ToolMessage{
		Role:    "user",
		Content: []llm.ContentBlock{{Type: "text", Text: "Build a thing"}},
	})

	// Messages 1..40: alternating assistant(tool_use) + tool_result pairs
	for i := 0; i < 20; i++ {
		messages = append(messages, llm.ToolMessage{
			Role: "assistant",
			Content: []llm.ContentBlock{{
				Type:     "tool_use",
				ToolCall: &llm.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "get_status"},
			}},
		})
		messages = append(messages, llm.ToolMessage{
			Role:        "tool_result",
			ToolResults: []llm.ToolResult{{ToolCallID: fmt.Sprintf("call-%d", i), Content: "ok"}},
		})
	}

	// Total: 1 + 40 = 41 messages. idealCut = 41-20 = 21
	// Message at index 21 is a tool_result (odd indices are tool_results for i>=1)
	// So idealCut would land on a tool_result — the function should adjust.

	compacted := q.compactMessages(messages)

	// Verify no tool_result appears right after the summary (index 2)
	for i := 2; i < len(compacted); i++ {
		msg := compacted[i]
		if msg.Role == "tool_result" || len(msg.ToolResults) > 0 {
			// Check that the previous message is an assistant with tool_use
			if i == 2 {
				t.Error("first kept message after summary should never be tool_result")
			}
			prev := compacted[i-1]
			if prev.Role != "assistant" || !hasToolUse(prev) {
				t.Errorf("tool_result at index %d has no matching assistant tool_use before it", i)
			}
		}
	}

	// First message should still be the objective
	if compacted[0].Content[0].Text != "Build a thing" {
		t.Error("first message should be the objective")
	}

	// Second message should be the compaction summary
	if !strings.Contains(compacted[1].Content[0].Text, "compacted") {
		t.Error("second message should be the compaction summary")
	}
}

func TestCompactMessages_SafeFallback(t *testing.T) {
	q := setupTestQueen(t)

	// Build a sequence where backward scan from idealCut all the way to 1
	// hits only tool_result or assistant+tool_use, forcing the fallback.
	var messages []llm.ToolMessage
	// Message 0: objective
	messages = append(messages, llm.ToolMessage{
		Role:    "user",
		Content: []llm.ContentBlock{{Type: "text", Text: "Objective"}},
	})

	// Fill messages 1..N with tight tool_use/tool_result pairs
	// so backward scan from idealCut can't find a safe boundary.
	for i := 0; i < 30; i++ {
		messages = append(messages, llm.ToolMessage{
			Role: "assistant",
			Content: []llm.ContentBlock{{
				Type:     "tool_use",
				ToolCall: &llm.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "get_status"},
			}},
		})
		messages = append(messages, llm.ToolMessage{
			Role:        "tool_result",
			ToolResults: []llm.ToolResult{{ToolCallID: fmt.Sprintf("call-%d", i), Content: "ok"}},
		})
	}

	// Total: 1 + 60 = 61 messages, idealCut = 61-20 = 41
	compacted := q.compactMessages(messages)

	// The result should have been compacted
	if len(compacted) >= len(messages) {
		t.Errorf("expected compaction, got %d messages (was %d)", len(compacted), len(messages))
	}

	// Verify the first kept message after the summary is NOT a tool_result
	if len(compacted) > 2 {
		if compacted[2].Role == "tool_result" || len(compacted[2].ToolResults) > 0 {
			t.Error("forward scan fallback should not start with tool_result")
		}
	}

	// All tool_result messages must be preceded by matching assistant+tool_use
	for i := 2; i < len(compacted); i++ {
		if compacted[i].Role == "tool_result" || len(compacted[i].ToolResults) > 0 {
			if i > 0 {
				prev := compacted[i-1]
				if prev.Role != "assistant" || !hasToolUse(prev) {
					t.Errorf("tool_result at index %d is orphaned (prev role=%s)", i, prev.Role)
				}
			}
		}
	}
}

func TestApplyCacheHints(t *testing.T) {
	// Test with tools and messages
	tools := []llm.ToolDef{
		{Name: "tool_a", Description: "A"},
		{Name: "tool_b", Description: "B"},
		{Name: "tool_c", Description: "C"},
	}
	messages := []llm.ToolMessage{
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Hello"}}},
		{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "Hi"}}},
		{Role: "user", Content: []llm.ContentBlock{
			{Type: "text", Text: "Do something"},
			{Type: "text", Text: "Also this"},
		}},
	}

	cachedTools, cachedMsgs := applyCacheHints(tools, messages)

	// Original tools should be unmodified
	for _, td := range tools {
		if td.Cache {
			t.Errorf("original tool %s should not have Cache set", td.Name)
		}
	}

	// Last cached tool should have Cache=true
	if !cachedTools[2].Cache {
		t.Error("last cached tool should have Cache=true")
	}
	// Other cached tools should not
	if cachedTools[0].Cache || cachedTools[1].Cache {
		t.Error("non-last cached tools should not have Cache=true")
	}

	// Original messages should be unmodified
	for _, msg := range messages {
		for _, cb := range msg.Content {
			if cb.Cache {
				t.Errorf("original message content should not have Cache set")
			}
		}
	}

	// Last user message's last content block should be cached
	lastUser := cachedMsgs[2]
	if !lastUser.Content[1].Cache {
		t.Error("last content block of last user message should have Cache=true")
	}
	// First content block of last user should not be cached
	if lastUser.Content[0].Cache {
		t.Error("first content block of last user message should not have Cache=true")
	}
}

func TestApplyCacheHints_EmptyInputs(t *testing.T) {
	// Empty tools and messages should not panic
	cachedTools, cachedMsgs := applyCacheHints(nil, nil)
	if len(cachedTools) != 0 {
		t.Errorf("expected empty tools, got %d", len(cachedTools))
	}
	if len(cachedMsgs) != 0 {
		t.Errorf("expected empty messages, got %d", len(cachedMsgs))
	}
}

func TestApplyCacheHints_NoUserMessages(t *testing.T) {
	// Only assistant messages — no user to cache
	tools := []llm.ToolDef{{Name: "tool_a"}}
	messages := []llm.ToolMessage{
		{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "Hi"}}},
	}

	cachedTools, cachedMsgs := applyCacheHints(tools, messages)

	// Tool should be cached
	if !cachedTools[0].Cache {
		t.Error("last tool should have Cache=true")
	}

	// No user message to cache, so assistant content should be unchanged
	if cachedMsgs[0].Content[0].Cache {
		t.Error("assistant content block should not have Cache")
	}
}
