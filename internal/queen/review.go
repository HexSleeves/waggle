package queen

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/exedev/waggle/internal/task"
)

// ReviewVerdict is the LLM's assessment of a completed task.
type ReviewVerdict struct {
	Approved    bool             `json:"approved"`
	Reason      string           `json:"reason"`
	Suggestions []string         `json:"suggestions,omitempty"`
	NewTasks    []TaskSuggestion `json:"new_tasks,omitempty"`
}

// TaskSuggestion is a follow-up task identified during review.
type TaskSuggestion struct {
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

// reviewWithLLM asks the LLM to evaluate a worker's output against the
// original task requirements and returns a structured verdict.
func (q *Queen) reviewWithLLM(ctx context.Context, taskID string, t *task.Task, result *task.Result) (*ReviewVerdict, error) {
	// llm client added in queen.go
	if q.llm == nil {
		// No LLM client configured — auto-approve so the existing
		// exit-code-based review path is unaffected.
		return &ReviewVerdict{
			Approved: true,
			Reason:   "LLM review not configured, auto-approved",
		}, nil
	}

	timeout := q.cfg.Queen.ReviewTimeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	systemPrompt := reviewSystemPrompt()
	userMessage := buildReviewPrompt(t, result)

	raw, err := q.llm.Chat(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, fmt.Errorf("review LLM call: %w", err)
	}

	verdict, err := parseReviewVerdict(raw)
	if err != nil {
		return nil, fmt.Errorf("parse review verdict: %w", err)
	}
	return verdict, nil
}

// reviewSystemPrompt returns the system-level instructions for the review LLM.
func reviewSystemPrompt() string {
	return `You are a strict code-review agent inside an automated orchestration system.
Your job is to evaluate whether a worker's output satisfies the original task.

Evaluate on three axes:
1. SCOPE — did the worker stay within the task description and constraints?
2. CORRECTNESS — is the output technically correct and complete?
3. FOLLOW-UP — are there obvious next steps or missing pieces?

Respond with ONLY a JSON object (no markdown fences, no commentary) using this schema:
{
  "approved": true|false,
  "reason": "one-paragraph explanation",
  "suggestions": ["actionable suggestion", ...],
  "new_tasks": [
    {
      "type": "code|test|review|research|generic",
      "title": "short title",
      "description": "what to do",
      "depends_on": ["task-id", ...]
    }
  ]
}`
}

const maxOutputChars = 8000

// buildReviewPrompt creates the user message for the review LLM call.
func buildReviewPrompt(t *task.Task, result *task.Result) string {
	var b strings.Builder

	b.WriteString("== ORIGINAL TASK ==\n")
	b.WriteString(fmt.Sprintf("ID:    %s\n", t.ID))
	b.WriteString(fmt.Sprintf("Type:  %s\n", t.Type))
	b.WriteString(fmt.Sprintf("Title: %s\n", t.Title))
	b.WriteString(fmt.Sprintf("Description:\n%s\n", t.GetDescription()))

	constraints := t.GetConstraints()
	if len(constraints) > 0 {
		b.WriteString("Constraints:\n")
		for _, c := range constraints {
			b.WriteString(fmt.Sprintf("  - %s\n", c))
		}
	}

	b.WriteString("\n== WORKER OUTPUT ==\n")
	if result == nil {
		b.WriteString("(no result returned)\n")
	} else {
		b.WriteString(fmt.Sprintf("Success: %v\n", result.Success))

		output := result.Output
		if len(output) > maxOutputChars {
			output = output[:maxOutputChars] + "\n... [truncated]\n"
		}
		b.WriteString(fmt.Sprintf("Output:\n%s\n", output))

		if len(result.Errors) > 0 {
			b.WriteString("Errors:\n")
			for _, e := range result.Errors {
				b.WriteString(fmt.Sprintf("  - %s\n", e))
			}
		}
	}

	b.WriteString("\n== INSTRUCTIONS ==\n")
	b.WriteString("Evaluate this output against the original task. Did the worker stay in scope? ")
	b.WriteString("Is the output correct and complete? Are there follow-up tasks needed?\n")
	b.WriteString("Respond with ONLY a JSON object matching the schema described in your instructions.\n")

	return b.String()
}

// parseReviewVerdict extracts a ReviewVerdict JSON object from potentially
// noisy LLM output. It is tolerant of markdown fences and surrounding text.
func parseReviewVerdict(raw string) (*ReviewVerdict, error) {
	raw = strings.TrimSpace(raw)

	// Strip markdown code fences if present.
	if idx := strings.Index(raw, "```"); idx != -1 {
		// Find opening fence end (skip language tag)
		start := strings.Index(raw[idx:], "\n")
		if start != -1 {
			inner := raw[idx+start+1:]
			if end := strings.Index(inner, "```"); end != -1 {
				raw = strings.TrimSpace(inner[:end])
			}
		}
	}

	// Find the outermost JSON object.
	start := strings.Index(raw, "{")
	if start == -1 {
		return nil, fmt.Errorf("no JSON object found in LLM output")
	}
	// Walk forward to find the matching closing brace.
	end := findMatchingBrace(raw, start)
	if end == -1 {
		return nil, fmt.Errorf("unbalanced braces in LLM output")
	}
	jsonStr := raw[start : end+1]

	var v ReviewVerdict
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return nil, fmt.Errorf("unmarshal review verdict: %w", err)
	}
	return &v, nil
}

// findMatchingBrace returns the index of the closing '}' that matches the
// opening '{' at position start, or -1 if not found. It respects strings.
func findMatchingBrace(s string, start int) int {
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
