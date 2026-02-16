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

// repairToolHistory fixes message history before sending to the LLM.
// It ensures every assistant tool_use has a matching tool_result,
// removes orphaned tool_results, and adds placeholder text to empty assistant messages.
func repairToolHistory(messages []llm.ToolMessage) []llm.ToolMessage {
	// Pass 1: Build a set of tool_use IDs from assistant messages, and
	// insert synthetic tool_results where they are missing.
	var repaired []llm.ToolMessage
	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		// Fix empty assistant content: add a placeholder text block.
		if msg.Role == "assistant" && len(msg.Content) == 0 {
			msg.Content = []llm.ContentBlock{{
				Type: "text",
				Text: "[continued]",
			}}
		}

		repaired = append(repaired, msg)

		// If this is an assistant message with tool_use blocks,
		// verify the next message is a tool_result with matching IDs.
		if msg.Role == "assistant" {
			var toolUseIDs []string
			for _, block := range msg.Content {
				if block.Type == "tool_use" && block.ToolCall != nil {
					toolUseIDs = append(toolUseIDs, block.ToolCall.ID)
				}
			}
			if len(toolUseIDs) == 0 {
				continue
			}

			// Collect IDs present in the next tool_result message (if any).
			resultIDs := make(map[string]bool)
			if i+1 < len(messages) && messages[i+1].Role == "tool_result" {
				for _, tr := range messages[i+1].ToolResults {
					resultIDs[tr.ToolCallID] = true
				}
			}

			// Build synthetic results for missing IDs.
			var missing []llm.ToolResult
			for _, id := range toolUseIDs {
				if !resultIDs[id] {
					missing = append(missing, llm.ToolResult{
						ToolCallID: id,
						Content:    "not executed (session interrupted); retry if needed",
						IsError:    true,
					})
				}
			}

			if len(missing) > 0 {
				if i+1 < len(messages) && messages[i+1].Role == "tool_result" {
					// Merge missing results into the existing tool_result.
					// We'll handle this by modifying the next message before it's appended.
					// Copy the next message, append missing, and skip original.
					next := messages[i+1]
					merged := make([]llm.ToolResult, len(next.ToolResults), len(next.ToolResults)+len(missing))
					copy(merged, next.ToolResults)
					merged = append(merged, missing...)
					repaired = append(repaired, llm.ToolMessage{
						Role:        "tool_result",
						ToolResults: merged,
					})
					i++ // skip original tool_result
				} else {
					// No tool_result message follows at all — insert one.
					repaired = append(repaired, llm.ToolMessage{
						Role:        "tool_result",
						ToolResults: missing,
					})
				}
			}
		}
	}

	// Pass 2: Remove orphaned tool_result entries whose IDs don't match
	// any preceding tool_use.
	precedingToolUseIDs := make(map[string]bool)
	var cleaned []llm.ToolMessage
	for _, msg := range repaired {
		// Collect tool_use IDs from assistant messages.
		if msg.Role == "assistant" {
			for _, block := range msg.Content {
				if block.Type == "tool_use" && block.ToolCall != nil {
					precedingToolUseIDs[block.ToolCall.ID] = true
				}
			}
			cleaned = append(cleaned, msg)
			continue
		}

		if msg.Role == "tool_result" && len(msg.ToolResults) > 0 {
			var kept []llm.ToolResult
			for _, tr := range msg.ToolResults {
				if precedingToolUseIDs[tr.ToolCallID] {
					kept = append(kept, tr)
				}
			}
			if len(kept) > 0 {
				cleaned = append(cleaned, llm.ToolMessage{
					Role:        "tool_result",
					ToolResults: kept,
				})
			}
			continue
		}

		cleaned = append(cleaned, msg)
	}

	return cleaned
}

// applyCacheHints sets cache flags on the last tool and last user message content block.
func applyCacheHints(tools []llm.ToolDef, messages []llm.ToolMessage) ([]llm.ToolDef, []llm.ToolMessage) {
	// Copy tools slice, set Cache on last tool
	if len(tools) > 0 {
		toolsCopy := make([]llm.ToolDef, len(tools))
		copy(toolsCopy, tools)
		toolsCopy[len(toolsCopy)-1].Cache = true
		tools = toolsCopy
	}

	// Find last user/tool_result message and set Cache on its last content block
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && len(messages[i].Content) > 0 {
			// Shallow copy the messages slice only around this index
			msgsCopy := make([]llm.ToolMessage, len(messages))
			copy(msgsCopy, messages)
			contentCopy := make([]llm.ContentBlock, len(messages[i].Content))
			copy(contentCopy, messages[i].Content)
			contentCopy[len(contentCopy)-1].Cache = true
			msgsCopy[i] = llm.ToolMessage{Role: messages[i].Role, Content: contentCopy, ToolResults: messages[i].ToolResults}
			messages = msgsCopy
			break
		}
	}
	return tools, messages
}

// toolTiming tracks aggregate timing for a single tool.
type toolTiming struct {
	calls    int
	totalDur time.Duration
}

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

	q.setObjective(objective)
	if !q.quiet {
		q.logger.Printf("🐝 Waggle Agent Mode | Objective: %s", objective)
	}

	// Preflight: verify and configure adapters
	if err := q.setupAdapters(ctx); err != nil {
		return err
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

	var totalUsage llm.Usage
	toolTimings := make(map[string]*toolTiming)

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

		// Repair history before sending to LLM
		messages = repairToolHistory(messages)

		// Call LLM with tools (with retry)
		cachedTools, cachedMsgs := applyCacheHints(tools, messages)
		resp, err := llm.RetryLLMCall(ctx, 3, q.logger, func() (*llm.Response, error) {
			return toolClient.ChatWithTools(ctx, systemPrompt, cachedMsgs, cachedTools)
		})
		if err != nil {
			return fmt.Errorf("queen LLM call failed: %w", err)
		}

		// Accumulate usage
		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens
		totalUsage.CacheCreationTokens += resp.Usage.CacheCreationTokens
		totalUsage.CacheReadTokens += resp.Usage.CacheReadTokens
		if !q.quiet {
			q.logger.Printf("  📊 Tokens: %d in / %d out", resp.Usage.InputTokens, resp.Usage.OutputTokens)
		}

		// Handle max_tokens truncation: do NOT append the truncated response
		if resp.StopReason == "max_tokens" {
			if !q.quiet {
				q.logger.Println("⚠ LLM response truncated (max_tokens), requesting retry")
			}
			messages = append(messages, llm.ToolMessage{
				Role: "user",
				Content: []llm.ContentBlock{{
					Type: "text",
					Text: "[SYSTEM: Your previous response was truncated because it exceeded the maximum output token limit. Any tool calls in that response were lost. Please retry with fewer tool calls or shorter text output.]",
				}},
			})
			continue
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

		if err := q.db.UpdateSessionPhase(ctx, q.sessionID, "agent", turn); err != nil {
			q.logger.Printf("⚠ Warning: failed to update session phase: %v", err)
		}

		// If no tool calls, the Queen is done talking
		if resp.StopReason == "end_turn" {
			if !q.quiet {
				q.logger.Println("👑 Queen ended conversation without calling complete()")
			}
			if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "done"); err != nil {
				q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
			}
			q.logRunSummary(totalUsage, toolTimings)
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

			toolStart := time.Now()
			result, toolErr := q.executeTool(ctx, tc)
			toolDuration := time.Since(toolStart)

			if !q.quiet {
				q.logger.Printf("  🔧 Tool: %s (%s)", tc.Name, toolDuration.Round(time.Millisecond))
			}

			if tt, ok := toolTimings[tc.Name]; ok {
				tt.calls++
				tt.totalDur += toolDuration
			} else {
				toolTimings[tc.Name] = &toolTiming{calls: 1, totalDur: toolDuration}
			}

			isError := toolErr != nil
			var content string
			if isError {
				content = toolErr.Error()
				if !q.quiet {
					q.logger.Printf("  ⚠ Tool error: %s", content)
				}
			} else {
				content = result.LLMContent
				// Log truncated result
				preview := result.DisplayContent()
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
				failReason = result.LLMContent
			}
		}

		// Append tool results to conversation
		if len(toolResults) > 0 {
			messages = append(messages, llm.ToolMessage{
				Role:        "tool_result",
				ToolResults: toolResults,
			})
		}

		// Persist full conversation (after both assistant + tool_result appended)
		q.persistConversation(ctx, messages)
		q.persistMessages(ctx, messages)

		// Handle terminal tools
		if completed {
			if !q.quiet {
				q.logger.Println("✅ Queen declared objective complete!")
			}
			if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "done"); err != nil {
				q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
			}
			q.logRunSummary(totalUsage, toolTimings)
			q.printReport()
			return nil
		}
		if failReason != "" {
			if !q.quiet {
				q.logger.Printf("❌ Queen declared failure: %s", failReason)
			}
			if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "failed"); err != nil {
				q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
			}
			q.logRunSummary(totalUsage, toolTimings)
			return fmt.Errorf("queen declared failure: %s", failReason)
		}

		// Context window management: compact if conversation is getting long
		if len(messages) > 80 {
			messages = q.compactMessages(messages)
		}
	}

	q.logRunSummary(totalUsage, toolTimings)
	if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "failed"); err != nil {
		q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
	}
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
	statusOutput, _ := handleGetStatus(ctx, q, nil)
	messages = append(messages, llm.ToolMessage{
		Role: "user",
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("Session resumed. Current task status:\n%s\n\nContinue working on the objective: %s",
				statusOutput.LLMContent, objective),
		}},
	})

	tools := queenTools()
	systemPrompt := q.buildSystemPrompt()
	maxTurns := q.cfg.Queen.MaxIterations

	var totalUsage llm.Usage
	toolTimings := make(map[string]*toolTiming)

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-ctx.Done():
			q.pool.KillAll()
			return ctx.Err()
		default:
		}

		// Repair history before sending to LLM
		messages = repairToolHistory(messages)

		cachedTools, cachedMsgs := applyCacheHints(tools, messages)
		resp, err := llm.RetryLLMCall(ctx, 3, q.logger, func() (*llm.Response, error) {
			return toolClient.ChatWithTools(ctx, systemPrompt, cachedMsgs, cachedTools)
		})
		if err != nil {
			return fmt.Errorf("queen LLM call failed: %w", err)
		}

		// Accumulate usage
		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens
		totalUsage.CacheCreationTokens += resp.Usage.CacheCreationTokens
		totalUsage.CacheReadTokens += resp.Usage.CacheReadTokens
		if !q.quiet {
			q.logger.Printf("  📊 Tokens: %d in / %d out", resp.Usage.InputTokens, resp.Usage.OutputTokens)
		}

		// Handle max_tokens truncation: do NOT append the truncated response
		if resp.StopReason == "max_tokens" {
			if !q.quiet {
				q.logger.Println("⚠ LLM response truncated (max_tokens), requesting retry")
			}
			messages = append(messages, llm.ToolMessage{
				Role: "user",
				Content: []llm.ContentBlock{{
					Type: "text",
					Text: "[SYSTEM: Your previous response was truncated because it exceeded the maximum output token limit. Any tool calls in that response were lost. Please retry with fewer tool calls or shorter text output.]",
				}},
			})
			continue
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

		if resp.StopReason == "end_turn" {
			q.persistConversation(ctx, messages)
			q.persistMessages(ctx, messages)
			if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "done"); err != nil {
				q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
			}
			q.logRunSummary(totalUsage, toolTimings)
			q.printReport()
			return nil
		}

		var toolResults []llm.ToolResult
		var completed bool

		for _, block := range resp.Content {
			if block.Type != "tool_use" || block.ToolCall == nil {
				continue
			}

			toolStart := time.Now()
			result, toolErr := q.executeTool(ctx, block.ToolCall)
			toolDuration := time.Since(toolStart)

			if !q.quiet {
				q.logger.Printf("  🔧 Tool: %s (%s)", block.ToolCall.Name, toolDuration.Round(time.Millisecond))
			}

			if tt, ok := toolTimings[block.ToolCall.Name]; ok {
				tt.calls++
				tt.totalDur += toolDuration
			} else {
				toolTimings[block.ToolCall.Name] = &toolTiming{calls: 1, totalDur: toolDuration}
			}

			isError := toolErr != nil
			var content string
			if isError {
				content = toolErr.Error()
			} else {
				content = result.LLMContent
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

		q.persistConversation(ctx, messages)
		q.persistMessages(ctx, messages)

		if completed {
			if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "done"); err != nil {
				q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
			}
			q.logRunSummary(totalUsage, toolTimings)
			q.printReport()
			return nil
		}

		if len(messages) > 80 {
			messages = q.compactMessages(messages)
		}
	}

	q.logRunSummary(totalUsage, toolTimings)
	if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "failed"); err != nil {
		q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
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

// persistMessages saves individual messages to the messages table.
func (q *Queen) persistMessages(ctx context.Context, messages []llm.ToolMessage) {
	for i, msg := range messages {
		content, _ := json.Marshal(msg)
		if err := q.db.AppendMessage(ctx, q.sessionID, i, msg.Role, string(content), ""); err != nil {
			q.logger.Printf("⚠ Warning: failed to persist message %d: %v", i, err)
		}
	}
}

// persistConversation saves the full conversation to the database for resume support.
func (q *Queen) persistConversation(ctx context.Context, messages []llm.ToolMessage) {
	data, err := json.Marshal(messages)
	if err != nil {
		q.logger.Printf("⚠ Failed to marshal conversation: %v", err)
		return
	}
	if err := q.db.SetKV(ctx, fmt.Sprintf("agent_conversation_%s", q.sessionID), string(data)); err != nil {
		q.logger.Printf("⚠ Warning: failed to persist conversation: %v", err)
	}
}

// loadConversation loads the full conversation from the database.
// Falls back to the legacy per-turn format for older sessions.
func (q *Queen) loadConversation(ctx context.Context, sessionID string) ([]llm.ToolMessage, error) {
	// New format: full conversation blob
	data, err := q.db.GetKV(ctx, fmt.Sprintf("agent_conversation_%s", sessionID))
	if err == nil && data != "" {
		var messages []llm.ToolMessage
		if err := json.Unmarshal([]byte(data), &messages); err == nil && len(messages) > 0 {
			return messages, nil
		}
	}

	// Legacy fallback: per-turn responses (assistant only, no tool_results)
	return q.loadConversationLegacy(ctx, sessionID)
}

// loadConversationLegacy reconstructs conversation from old per-turn format.
func (q *Queen) loadConversationLegacy(ctx context.Context, sessionID string) ([]llm.ToolMessage, error) {
	var messages []llm.ToolMessage

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

	for i := 0; i < 1000; i++ {
		data, err := q.db.GetKV(ctx, fmt.Sprintf("agent_turn_%s_%d", sessionID, i))
		if err != nil || data == "" {
			break
		}

		var resp llm.Response
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			continue
		}

		messages = append(messages, llm.ToolMessage{
			Role:    "assistant",
			Content: resp.Content,
		})
	}

	return messages, nil
}

// compactMessages reduces the conversation size by summarizing older turns.
// Keeps the first message (objective) and the last N messages intact.
// Never splits tool_use/tool_result pairs.
func (q *Queen) compactMessages(messages []llm.ToolMessage) []llm.ToolMessage {
	keepLast := 20

	if len(messages) <= keepLast+2 {
		return messages
	}

	// Find a safe cut point that doesn't split tool_use/tool_result pairs.
	idealCut := len(messages) - keepLast
	cutPoint := idealCut
	for cutPoint > 1 {
		msg := messages[cutPoint]
		// Don't start the kept section with a tool_result
		if msg.Role == "tool_result" || len(msg.ToolResults) > 0 {
			cutPoint--
			continue
		}
		// Don't cut right after an assistant message with tool_use
		if cutPoint > 0 {
			prev := messages[cutPoint-1]
			if prev.Role == "assistant" && hasToolUse(prev) {
				cutPoint--
				continue
			}
		}
		break
	}
	if cutPoint <= 1 {
		// Scan forward from idealCut to find a safe boundary
		cutPoint = idealCut
		for cutPoint < len(messages)-1 {
			msg := messages[cutPoint]
			if msg.Role == "tool_result" || len(msg.ToolResults) > 0 {
				cutPoint++
				continue
			}
			if cutPoint > 0 {
				prev := messages[cutPoint-1]
				if prev.Role == "assistant" && hasToolUse(prev) {
					cutPoint++
					continue
				}
			}
			break
		}
	}

	// Build summary of compacted section
	var summary strings.Builder
	summary.WriteString("[Conversation history compacted. Summary of earlier turns:]\n")

	toolCallCount := 0
	for _, msg := range messages[1:cutPoint] {
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
	summary.WriteString(fmt.Sprintf("\n[%d tool calls in compacted section]\n", toolCallCount))

	compacted := make([]llm.ToolMessage, 0, len(messages)-cutPoint+2)
	compacted = append(compacted, messages[0]) // objective
	compacted = append(compacted, llm.ToolMessage{
		Role: "user",
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: summary.String(),
		}},
	})
	compacted = append(compacted, messages[cutPoint:]...)

	// Verify: the first kept message after the summary must not be tool_result
	if len(compacted) > 2 {
		if compacted[2].Role == "tool_result" || len(compacted[2].ToolResults) > 0 {
			q.logger.Printf("⚠ compactMessages: first kept message is tool_result, adjusting")
			// Skip tool_result messages at the start of the kept section
			idx := 2
			for idx < len(compacted) && (compacted[idx].Role == "tool_result" || len(compacted[idx].ToolResults) > 0) {
				idx++
			}
			compacted = append(compacted[:2], compacted[idx:]...)
		}
	}

	if !q.quiet {
		q.logger.Printf("📦 Compacted conversation: %d → %d messages", len(messages), len(compacted))
	}
	return compacted
}

// hasToolUse returns true if the message contains any tool_use content blocks.
func hasToolUse(msg llm.ToolMessage) bool {
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

// logRunSummary logs total usage and tool timing at the end of a run.
func (q *Queen) logRunSummary(totalUsage llm.Usage, toolTimings map[string]*toolTiming) {
	if q.quiet {
		return
	}
	if totalUsage.InputTokens > 0 || totalUsage.OutputTokens > 0 {
		q.logger.Printf("📊 Total tokens: %dk in / %dk out", totalUsage.InputTokens/1000, totalUsage.OutputTokens/1000)
		if totalUsage.CacheReadTokens > 0 {
			q.logger.Printf("   Cache: %dk created / %dk read", totalUsage.CacheCreationTokens/1000, totalUsage.CacheReadTokens/1000)
		}
	}
	if len(toolTimings) > 0 {
		q.logger.Println("⏱️ Tool timing summary:")
		for name, tt := range toolTimings {
			avg := tt.totalDur / time.Duration(tt.calls)
			q.logger.Printf("   %s: %d calls, avg %s, total %s", name, tt.calls, avg.Round(time.Millisecond), tt.totalDur.Round(time.Millisecond))
		}
	}
}

// SetupSignalHandler sets up graceful shutdown on SIGINT/SIGTERM.
// Returns a cleanup function that stops signal delivery and should be deferred.
func SetupSignalHandler(cancel context.CancelFunc) func() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()
	return func() { signal.Stop(sigs) }
}
