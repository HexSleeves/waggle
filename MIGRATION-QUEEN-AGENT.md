# Migration Plan: Queen as Tool-Using LLM Agent

> Created: 2026-02-15

## Goal

Transform the Queen from a **Go-coded state machine** (fixed Plan→Delegate→Monitor→Review→Replan loop) into a **persistent LLM agent with tools** that autonomously decides how to orchestrate workers.

The existing infrastructure (TaskGraph, Pool, Adapters, Safety, Blackboard, DB) becomes the **runtime** that the Queen's tools invoke.

## Architecture: Before vs After

```
BEFORE (hardcoded loop):

  Run() → plan() → delegate() → monitor() → review() → replan() → loop
           ↑ LLM     ↑ Go code    ↑ polling    ↑ LLM      ↑ LLM

  The Queen's intelligence is scattered across 5 fixed phases.
  She can only act at predetermined points. Decision logic is in Go.

AFTER (autonomous agent):

  Run() → agent loop:
    1. Build state context (tasks, workers, recent events)
    2. Send to LLM with tool definitions
    3. LLM responds with tool calls or text
    4. Execute tools, feed results back
    5. Repeat until LLM calls complete()

  The Queen decides what to do and when. All orchestration
  logic lives in the system prompt. Go just executes tools.
```

## Phases

### Phase 1: Extend LLM Client with Tool Use Support
**Files:** `internal/llm/client.go`, `internal/llm/anthropic.go`, `internal/llm/types.go` (new)
**Depends on:** Nothing
**Risk:** Low

Extend the `llm.Client` interface to support tool-use conversations:

```go
// types.go — new file
type ToolDef struct {
    Name        string
    Description string
    InputSchema map[string]interface{} // JSON Schema
}

type ToolCall struct {
    ID    string
    Name  string
    Input json.RawMessage
}

type ToolResult struct {
    ToolCallID string
    Content    string
    IsError    bool
}

type ContentBlock struct {
    Type      string      // "text" or "tool_use"
    Text      string      // if type=text
    ToolCall  *ToolCall   // if type=tool_use
}

type Response struct {
    Content    []ContentBlock
    StopReason string // "end_turn", "tool_use", "max_tokens"
}

// Extended message types for the conversation
type MessageContent struct {
    Role    string        // "user", "assistant", "tool_result"
    Content []ContentBlock
    ToolResults []ToolResult  // when Role="tool_result"
}
```

Add a new method to the `Client` interface:
```go
type ToolClient interface {
    Client // embed existing
    ChatWithTools(ctx context.Context, systemPrompt string,
        messages []MessageContent, tools []ToolDef) (*Response, error)
}
```

Implement for `AnthropicClient` using the SDK's existing tool-use support:
- `ToolUnionParamOfTool()` for definitions
- `NewToolUseBlock()` / `NewToolResultBlock()` for conversation
- Handle `StopReason == "tool_use"` vs `"end_turn"`

For `CLIClient`: tools are not supported (CLI tools don't do structured tool use). Return `ErrToolsNotSupported`. The Queen falls back to the legacy loop for non-Anthropic providers.

**Tests:** Unit test with mock HTTP server verifying tool call/result round-trip.

---

### Phase 2: Define Queen's Tool Schemas
**Files:** `internal/queen/tools.go` (new)
**Depends on:** Phase 1
**Risk:** Low

Define the tools the Queen can call:

| Tool | Input | What it does |
|------|-------|--------------|
| `create_tasks` | `{tasks: [{id, title, description, type, depends_on, constraints, allowed_paths}]}` | Add tasks to the TaskGraph |
| `assign_task` | `{task_id: string}` | Spawn a worker via adapter and assign the task |
| `get_status` | `{}` | Return all tasks with current status, worker assignments |
| `get_task_output` | `{task_id: string}` | Return the completed/failed task's worker output |
| `approve_task` | `{task_id: string, feedback?: string}` | Mark a task as approved |
| `reject_task` | `{task_id: string, feedback: string}` | Reject, re-queue with feedback appended |
| `wait_for_workers` | `{timeout_seconds?: int}` | Block until at least one running worker completes |
| `message_worker` | `{task_id: string, message: string}` | Post a message to the blackboard for a worker |
| `read_file` | `{path: string, line_start?: int, line_end?: int}` | Read a project file (safety-checked) |
| `list_files` | `{path?: string, pattern?: string}` | List files in project directory |
| `complete` | `{summary: string}` | Declare the objective done, end the session |
| `fail` | `{reason: string}` | Declare the objective failed |

Each tool is defined as a `llm.ToolDef` with a JSON Schema `InputSchema`.

**Implementation:** Each tool maps to a handler function:
```go
type ToolHandler func(ctx context.Context, q *Queen, input json.RawMessage) (string, error)

var toolHandlers = map[string]ToolHandler{
    "create_tasks":    handleCreateTasks,
    "assign_task":     handleAssignTask,
    "get_status":      handleGetStatus,
    // ...
}
```

Handlers reuse existing Queen methods: `q.tasks.Add()`, `q.pool.Acquire()`, `q.board.Post()`, etc.

**Tests:** Test each handler in isolation with a mock Queen.

---

### Phase 3: Queen System Prompt
**Files:** `internal/queen/prompt.go` (new)
**Depends on:** Phase 2
**Risk:** Low

Craft the Queen's system prompt:

```
You are the Queen Bee, an AI orchestration agent. You manage a team of
worker bees (AI coding agents) to accomplish a user's objective.

Your workflow:
1. Analyze the objective and break it into tasks (create_tasks)
2. Assign tasks to workers respecting dependencies (assign_task)
3. Wait for workers to complete (wait_for_workers) 
4. Review each completed task's output (get_task_output)
5. Approve good work or reject with feedback (approve_task/reject_task)
6. Decide if more tasks are needed or if the objective is met
7. When done, call complete() with a summary

Rules:
- Tasks should be narrowly scoped — one concern per task
- Respect dependency ordering — don't assign tasks with unmet deps
- Workers are AI coding agents — give them clear, actionable descriptions
- If a worker's output is wrong, reject with specific feedback
- You can read project files to understand context (read_file, list_files)
- Call get_status frequently to track progress
- Maximum {max_iterations} tool-use turns

Current adapter: {adapter_name}
Project directory: {project_dir}
Max parallel workers: {max_parallel}
```

**Tests:** Template rendering with various configs.

---

### Phase 4: Agent Loop (Core Rewrite)
**Files:** `internal/queen/agent.go` (new), `internal/queen/queen.go` (modified)
**Depends on:** Phases 1-3
**Risk:** High — this is the core change

New `RunAgent()` method on Queen:

```go
func (q *Queen) RunAgent(ctx context.Context, objective string) error {
    toolClient, ok := q.llm.(llm.ToolClient)
    if !ok {
        // Fallback to legacy loop for non-tool providers
        return q.Run(ctx, objective)
    }

    // Initialize session
    q.sessionID = newSessionID()
    q.db.CreateSession(ctx, q.sessionID, objective)

    // Build initial message
    messages := []llm.MessageContent{{
        Role: "user",
        Content: []llm.ContentBlock{{
            Type: "text",
            Text: fmt.Sprintf("Objective: %s", objective),
        }},
    }}

    tools := q.toolDefs()
    systemPrompt := q.buildSystemPrompt()

    for turn := 0; turn < q.cfg.Queen.MaxIterations; turn++ {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        // Call LLM
        resp, err := toolClient.ChatWithTools(ctx, systemPrompt, messages, tools)
        if err != nil {
            return fmt.Errorf("queen LLM call failed: %w", err)
        }

        // Append assistant response to conversation
        messages = append(messages, llm.MessageContent{
            Role:    "assistant",
            Content: resp.Content,
        })

        // Log any text output
        for _, block := range resp.Content {
            if block.Type == "text" && block.Text != "" {
                q.logger.Printf("👑 Queen: %s", block.Text)
            }
        }

        // If no tool calls, the Queen is done talking
        if resp.StopReason == "end_turn" {
            q.db.UpdateSessionStatus(ctx, q.sessionID, "done")
            return nil
        }

        // Execute tool calls
        var toolResults []llm.ToolResult
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

            // Check if the "complete" tool was called
            if block.ToolCall.Name == "complete" {
                q.db.UpdateSessionStatus(ctx, q.sessionID, "done")
                q.printReport()
                return nil
            }
            if block.ToolCall.Name == "fail" {
                q.db.UpdateSessionStatus(ctx, q.sessionID, "failed")
                return fmt.Errorf("queen declared failure: %s", content)
            }
        }

        // Append tool results to conversation
        messages = append(messages, llm.MessageContent{
            Role:        "tool_result",
            ToolResults: toolResults,
        })

        // Persist conversation state
        q.db.UpdateSessionPhase(ctx, q.sessionID, "agent", turn)
    }

    return fmt.Errorf("queen exceeded max iterations (%d)", q.cfg.Queen.MaxIterations)
}
```

**Key decisions:**
- The old `Run()` method stays as fallback for CLI-based LLM providers
- The new `RunAgent()` is used when the provider supports tool use
- The entry point (`cmd/waggle`) picks the right method based on provider
- `wait_for_workers` tool is the only blocking tool — it polls the worker pool and returns when a worker completes
- Each tool call result feeds back into the conversation
- The LLM sees the full conversation history (with compaction at threshold)

**Tests:**
- Mock ToolClient that returns scripted tool calls
- Test full cycle: create_tasks → assign → wait → get_output → approve → complete
- Test rejection/retry flow
- Test max iterations limit
- Test context cancellation

---

### Phase 5: Wire Up and CLI Changes
**Files:** `cmd/waggle/commands.go`, `internal/queen/queen.go`
**Depends on:** Phase 4
**Risk:** Low

- Update `cmd/waggle/commands.go` to call `RunAgent()` when tool-use is available
- Add `--legacy` flag to force the old loop (useful for debugging)
- Ensure `resume` works with the agent loop (restore conversation history from DB)
- Update status command to show agent turns instead of phases

---

### Phase 6: Persist Agent Conversation
**Files:** `internal/state/db.go`, `internal/queen/agent.go`
**Depends on:** Phase 4
**Risk:** Medium

Store the full LLM conversation in SQLite for:
- Session resume (reload messages array)
- Debugging (see exactly what the Queen decided and why)
- Audit trail

New DB table:
```sql
CREATE TABLE agent_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    turn INTEGER NOT NULL,
    role TEXT NOT NULL,           -- user, assistant, tool_result
    content_json TEXT NOT NULL,   -- full message content as JSON
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);
```

---

### Phase 7: Context Window Management
**Files:** `internal/queen/agent.go`
**Depends on:** Phase 6
**Risk:** Medium

The conversation grows with each tool call. Manage it:
- Track approximate token count (~4 chars/token)
- When approaching limit (e.g., 150k tokens for Sonnet), compact:
  - Keep system prompt + last N turns
  - Summarize earlier turns into a "conversation so far" user message
  - Preserve all task status (get_status is cheap to re-call)
- Leverage the existing `compact.Context` module

---

## Execution Order

```
Phase 1 (LLM tool use)     ← Foundation, no risk to existing code
  │
  ├── Phase 2 (tool schemas) ← Parallel with Phase 3
  │     │
  │     ├── Phase 3 (system prompt)
  │     │
  │     └── Phase 4 (agent loop) ← The big one
  │           │
  │           ├── Phase 5 (CLI wiring)
  │           │
  │           └── Phase 6 (conversation persistence)
  │                 │
  │                 └── Phase 7 (context management)
  │
  └── [Old Run() stays as fallback — zero regression risk]
```

## What We Keep

| Component | Status |
|-----------|--------|
| TaskGraph + cycle detection | Keep — tools call into it |
| Worker Pool + Bee interface | Keep — `assign_task` tool spawns via pool |
| Adapter Registry + Router | Keep — tool handler uses router to pick adapter |
| Safety Guard | Keep — enforced in `assign_task` and `read_file` tools |
| Blackboard | Keep — `message_worker` posts to it |
| SQLite DB + events | Keep — all tools persist state |
| Bus (pub/sub) | Keep — events still fire |
| Error classification + retry | Keep — `reject_task` tool reuses retry logic |
| Config system | Keep — adds `queen.agent_mode` flag |

## What Changes

| Component | Change |
|-----------|--------|
| `queen.Run()` | Stays as legacy fallback |
| `queen.review()` | Replaced by LLM's judgment via `approve_task`/`reject_task` |
| `queen.replan()` | Replaced by LLM's autonomous decision to `create_tasks` |
| `queen.plan()` | Replaced by LLM calling `create_tasks` |
| `queen.delegate()` | Replaced by LLM calling `assign_task` |
| `queen.monitor()` | Replaced by `wait_for_workers` tool |
| `review.go` | Kept for legacy mode, not used in agent mode |
| `replan.go` | Kept for legacy mode, not used in agent mode |

## Configuration: Queen vs Workers

The Queen and workers are **independently configurable**:

```json
{
  "queen": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514"
  },
  "workers": {
    "default_adapter": "kimi",
    "max_parallel": 4
  }
}
```

| Setting | What it controls | Examples |
|---------|-----------------|----------|
| `queen.provider` | Queen's reasoning engine | `anthropic`, `openai`, `kimi`, `codex`, `gemini`, `claude-cli` |
| `workers.default_adapter` | CLI tool workers run as | `kimi`, `claude-code`, `codex`, `opencode`, `gemini`, `exec` |

**Any combination works.** Examples:
- `queen=anthropic + workers=kimi` → Queen reasons via API, workers code via Kimi (cheapest)
- `queen=anthropic + workers=claude-code` → Full Anthropic stack
- `queen=anthropic + workers=exec` → Queen orchestrates shell commands
- `queen=kimi + workers=kimi` → All-Kimi (legacy loop, no tool use)
- `queen=codex + workers=kimi` → Codex plans, Kimi executes (legacy loop)

**Agent loop** (tool use) requires a provider with structured tool support: `anthropic`, `openai` (future).
**Legacy loop** (fixed phases) works with any provider, including CLI-based ones.

The system auto-detects: if `queen.provider` implements `ToolClient`, use agent loop. Otherwise, fall back to legacy.

## Risks

1. **Token cost** — Each tool call round-trip costs tokens. Mitigate with efficient state formatting and compaction.
2. **Latency** — Each LLM call adds ~2-5s. Mitigate by batching (create multiple tasks in one call, assign multiple in one call).
3. **Hallucinated tool calls** — LLM may call tools with invalid inputs. Mitigate with input validation in every handler.
4. **Context overflow** — Long sessions fill the context window. Mitigate with Phase 7 compaction.
5. **Provider lock-in** — Tool use only works with Anthropic initially. Mitigate by keeping legacy loop as fallback.

## Success Criteria

- [ ] `waggle --provider anthropic run "Review this codebase"` uses the agent loop
- [ ] Queen autonomously plans, delegates, monitors, reviews, and completes
- [ ] Failed tasks get rejected with feedback and retried
- [ ] `waggle --legacy run "..."` still works with the old loop
- [ ] CLI-based providers (kimi, gemini) fall back to legacy loop automatically
- [ ] Session can be resumed mid-conversation
- [ ] All existing tests still pass
