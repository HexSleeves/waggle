package queen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/exedev/waggle/internal/llm"
)

// RunAgent executes the Queen as an autonomous tool-using LLM agent.
// The LLM decides what tools to call and when. The Go code just executes tool
// calls and feeds results back.
//
// Falls back to the legacy Run() loop if the LLM provider doesn't support tools.
func (q *Queen) RunAgent(ctx context.Context, objective string) error {
	toolClient, ok := q.llm.(llm.ToolClient)
	if !ok {
		if !q.quiet {
			q.logger.Println("⚠ LLM provider does not support tool use, falling back to legacy loop")
		}
		return q.Run(ctx, objective)
	}

	q.objective = objective
	if !q.quiet {
		q.logger.Printf("🐝 Waggle Agent Mode | Objective: %s", objective)
	}

	// Preflight: verify adapters
	available := q.registry.Available()
	if len(available) == 0 {
		return fmt.Errorf("no adapters available — install claude, codex, or opencode CLI")
	}
	defaultAdapter := q.cfg.Workers.DefaultAdapter
	if a, ok := q.registry.Get(defaultAdapter); !ok || !a.Available() {
		if !q.quiet {
			q.logger.Printf("⚠ Default adapter %q not available, falling back to: %s", defaultAdapter, available[0])
		}
	}
	// Filter exec from display unless it's the chosen adapter
	displayAdapters := make([]string, 0, len(available))
	for _, name := range available {
		if name != "exec" || defaultAdapter == "exec" {
			displayAdapters = append(displayAdapters, name)
		}
	}
	if !q.quiet {
		q.logger.Printf("✓ Using adapter: %s | Available: %v", defaultAdapter, displayAdapters)
	}

	// Create DB session
	q.sessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
	if err := q.db.CreateSession(ctx, q.sessionID, objective); err != nil {
		if !q.quiet {
			q.logger.Printf("⚠ DB: failed to create session: %v", err)
		}
	}

	// Build initial conversation
	messages := []llm.ToolMessage{{
		Role: "user",
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("Objective: %s", objective),
		}},
	}}

	tools := queenTools()
	systemPrompt := q.buildSystemPrompt()

	// Agent loop
	maxTurns := q.cfg.Queen.MaxIterations
	if maxTurns <= 0 {
		maxTurns = 50
	}

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-ctx.Done():
			if !q.quiet {
				q.logger.Println("⛔ Context cancelled, shutting down")
			}
			q.pool.KillAll()
			return ctx.Err()
		default:
		}

		if !q.quiet {
			q.logger.Printf("\n━━━ Agent Turn %d/%d ━━━", turn+1, maxTurns)
		}

		// Call LLM with tools
		resp, err := toolClient.ChatWithTools(ctx, systemPrompt, messages, tools)
		if err != nil {
			// If the error is transient, retry once
			if !q.quiet {
				q.logger.Printf("⚠ LLM call failed: %v, retrying...", err)
			}
			time.Sleep(2 * time.Second)
			resp, err = toolClient.ChatWithTools(ctx, systemPrompt, messages, tools)
			if err != nil {
				return fmt.Errorf("queen LLM call failed: %w", err)
			}
		}

		// Append assistant response to conversation
		messages = append(messages, llm.ToolMessage{
			Role:    "assistant",
			Content: resp.Content,
		})

		// Log any text output from the Queen
		for _, block := range resp.Content {
			if block.Type == "text" && block.Text != "" {
				if !q.quiet {
					q.logger.Printf("👑 Queen: %s", block.Text)
				}
			}
		}

		// Persist the turn
		q.db.UpdateSessionPhase(ctx, q.sessionID, "agent", turn)
		q.persistTurn(ctx, turn, resp)

		// If no tool calls, the Queen is done talking
		if resp.StopReason == "end_turn" {
			if !q.quiet {
				q.logger.Println("👑 Queen ended conversation without calling complete()")
			}
			q.db.UpdateSessionStatus(ctx, q.sessionID, "done")
			q.printReport()
			return nil
		}

		// Execute tool calls
		var toolResults []llm.ToolResult
		var completed bool
		var failReason string

		for _, block := range resp.Content {
			if block.Type != "tool_use" || block.ToolCall == nil {
				continue
			}

			tc := block.ToolCall
			if !q.quiet {
				q.logger.Printf("  🔧 Tool: %s", tc.Name)
			}

			result, toolErr := q.executeTool(ctx, tc)
			isError := toolErr != nil
			content := result
			if isError {
				content = toolErr.Error()
				if !q.quiet {
					q.logger.Printf("  ⚠ Tool error: %s", content)
				}
			} else {
				// Log truncated result
				preview := content
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				if !q.quiet {
					q.logger.Printf("  ✓ Result: %s", preview)
				}
			}

			toolResults = append(toolResults, llm.ToolResult{
				ToolCallID: tc.ID,
				Content:    content,
				IsError:    isError,
			})

			// Check for terminal tools
			if tc.Name == "complete" && !isError {
				completed = true
			}
			if tc.Name == "fail" && !isError {
				failReason = content
			}
		}

		// Append tool results to conversation
		if len(toolResults) > 0 {
			messages = append(messages, llm.ToolMessage{
				Role:        "tool_result",
				ToolResults: toolResults,
			})
		}

		// Handle terminal tools
		if completed {
			if !q.quiet {
				q.logger.Println("✅ Queen declared objective complete!")
			}
			q.db.UpdateSessionStatus(ctx, q.sessionID, "done")
			q.printReport()
			return nil
		}
		if failReason != "" {
			if !q.quiet {
				q.logger.Printf("❌ Queen declared failure: %s", failReason)
			}
			q.db.UpdateSessionStatus(ctx, q.sessionID, "failed")
			return fmt.Errorf("queen declared failure: %s", failReason)
		}

		// Context window management: compact if conversation is getting long
		if len(messages) > 80 {
			messages = q.compactMessages(messages)
		}
	}

	q.db.UpdateSessionStatus(ctx, q.sessionID, "failed")
	return fmt.Errorf("queen exceeded max turns (%d)", maxTurns)
}

// RunAgentResume resumes an agent-mode session from a previous conversation.
func (q *Queen) RunAgentResume(ctx context.Context, sessionID string) error {
	objective, err := q.ResumeSession(ctx, sessionID)
	if err != nil {
		return err
	}

	// Load persisted conversation
	messages, err := q.loadConversation(ctx, sessionID)
	if err != nil || len(messages) == 0 {
		// Can't restore conversation — just re-run with the same objective
		if !q.quiet {
			q.logger.Println("⚠ Could not restore conversation, restarting agent loop")
		}
		return q.RunAgent(ctx, objective)
	}

	toolClient, ok := q.llm.(llm.ToolClient)
	if !ok {
		return q.Run(ctx, objective)
	}

	if !q.quiet {
		q.logger.Printf("🔄 Resuming agent session %s with %d messages", sessionID, len(messages))
	}

	// Inject a status update so the Queen knows where things stand
	statusJSON, _ := handleGetStatus(ctx, q, nil)
	messages = append(messages, llm.ToolMessage{
		Role: "user",
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("Session resumed. Current task status:\n%s\n\nContinue working on the objective: %s",
				statusJSON, objective),
		}},
	})

	tools := queenTools()
	systemPrompt := q.buildSystemPrompt()
	maxTurns := q.cfg.Queen.MaxIterations

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-ctx.Done():
			q.pool.KillAll()
			return ctx.Err()
		default:
		}

		resp, err := toolClient.ChatWithTools(ctx, systemPrompt, messages, tools)
		if err != nil {
			return fmt.Errorf("queen LLM call failed: %w", err)
		}

		messages = append(messages, llm.ToolMessage{
			Role:    "assistant",
			Content: resp.Content,
		})

		for _, block := range resp.Content {
			if block.Type == "text" && block.Text != "" {
				if !q.quiet {
					q.logger.Printf("👑 Queen: %s", block.Text)
				}
			}
		}

		q.persistTurn(ctx, turn, resp)

		if resp.StopReason == "end_turn" {
			q.db.UpdateSessionStatus(ctx, q.sessionID, "done")
			q.printReport()
			return nil
		}

		var toolResults []llm.ToolResult
		var completed bool

		for _, block := range resp.Content {
			if block.Type != "tool_use" || block.ToolCall == nil {
				continue
			}

			result, toolErr := q.executeTool(ctx, block.ToolCall)
			isError := toolErr != nil
			content := result
			if isError {
				content = toolErr.Error()
			}

			toolResults = append(toolResults, llm.ToolResult{
				ToolCallID: block.ToolCall.ID,
				Content:    content,
				IsError:    isError,
			})

			if block.ToolCall.Name == "complete" && !isError {
				completed = true
			}
		}

		if len(toolResults) > 0 {
			messages = append(messages, llm.ToolMessage{
				Role:        "tool_result",
				ToolResults: toolResults,
			})
		}

		if completed {
			q.db.UpdateSessionStatus(ctx, q.sessionID, "done")
			q.printReport()
			return nil
		}

		if len(messages) > 80 {
			messages = q.compactMessages(messages)
		}
	}

	return fmt.Errorf("queen exceeded max turns (%d)", maxTurns)
}

// SupportsAgentMode returns true if the Queen's LLM supports tool use.
func (q *Queen) SupportsAgentMode() bool {
	if q.llm == nil {
		return false
	}
	_, ok := q.llm.(llm.ToolClient)
	return ok
}

// persistTurn saves a conversation turn to the database for resume support.
func (q *Queen) persistTurn(ctx context.Context, turn int, resp *llm.Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		q.logger.Printf("⚠ Failed to marshal turn: %v", err)
		return
	}
	q.db.SetKV(ctx, fmt.Sprintf("agent_turn_%s_%d", q.sessionID, turn), string(data))
}

// loadConversation reconstructs the message history from persisted turns.
func (q *Queen) loadConversation(ctx context.Context, sessionID string) ([]llm.ToolMessage, error) {
	var messages []llm.ToolMessage

	// Get the objective as the first user message
	session, err := q.db.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	messages = append(messages, llm.ToolMessage{
		Role: "user",
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("Objective: %s", session.Objective),
		}},
	})

	// Load persisted turns
	for i := 0; i < 1000; i++ {
		data, err := q.db.GetKV(ctx, fmt.Sprintf("agent_turn_%s_%d", sessionID, i))
		if err != nil || data == "" {
			break
		}

		var resp llm.Response
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			continue
		}

		// Add assistant message
		messages = append(messages, llm.ToolMessage{
			Role:    "assistant",
			Content: resp.Content,
		})
	}

	return messages, nil
}

// compactMessages reduces the conversation size by summarizing older turns.
// Keeps the first message (objective) and the last N messages intact.
func (q *Queen) compactMessages(messages []llm.ToolMessage) []llm.ToolMessage {
	keepLast := 20 // keep last 20 messages for context

	if len(messages) <= keepLast+2 {
		return messages
	}

	// Build a summary of the middle section
	var summary strings.Builder
	summary.WriteString("[Conversation history compacted. Summary of earlier turns:]\n")

	middleStart := 1 // skip the first (objective) message
	middleEnd := len(messages) - keepLast

	toolCallCount := 0
	for _, msg := range messages[middleStart:middleEnd] {
		for _, block := range msg.Content {
			if block.Type == "tool_use" && block.ToolCall != nil {
				toolCallCount++
				summary.WriteString(fmt.Sprintf("- Called %s\n", block.ToolCall.Name))
			}
			if block.Type == "text" && block.Text != "" && msg.Role == "assistant" {
				text := block.Text
				if len(text) > 150 {
					text = text[:150] + "..."
				}
				summary.WriteString(fmt.Sprintf("- Queen said: %s\n", text))
			}
		}
	}

	summary.WriteString(fmt.Sprintf("\n[%d tool calls were made in the compacted section]\n", toolCallCount))

	// Reconstruct: objective + summary + last N messages
	compacted := make([]llm.ToolMessage, 0, keepLast+2)
	compacted = append(compacted, messages[0]) // objective
	compacted = append(compacted, llm.ToolMessage{
		Role: "user",
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: summary.String(),
		}},
	})
	compacted = append(compacted, messages[middleEnd:]...)

	if !q.quiet {
		q.logger.Printf("📦 Compacted conversation: %d → %d messages", len(messages), len(compacted))
	}
	return compacted
}

// SetupSignalHandler sets up graceful shutdown on SIGINT/SIGTERM.
// Returns a cancel function that should be deferred.
func SetupSignalHandler(cancel context.CancelFunc) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()
}
