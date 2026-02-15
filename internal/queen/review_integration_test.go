package queen

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/blackboard"
	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/config"
	"github.com/exedev/waggle/internal/llm"
	"github.com/exedev/waggle/internal/state"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// mockReviewClient is a mock LLM client for testing review functionality
type mockReviewClient struct {
	response string
	err      error
}

func (m *mockReviewClient) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	return m.response, m.err
}

func (m *mockReviewClient) ChatWithHistory(ctx context.Context, systemPrompt string, messages []llm.Message) (string, error) {
	return m.response, m.err
}

// reviewTestQueen creates a Queen with a mock LLM client for review testing
func reviewTestQueen(t *testing.T, mockClient *mockReviewClient) *Queen {
	t.Helper()
	tmpDir := t.TempDir()

	hiveDir := tmpDir + "/.hive"

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	msgBus := bus.New(100)
	board := blackboard.New(msgBus)
	tasks := task.NewTaskGraph(msgBus)

	pool := worker.NewPool(4, func(id, adapter string) (worker.Bee, error) {
		return nil, nil
	}, msgBus)

	cfg := &config.Config{
		ProjectDir: tmpDir,
		HiveDir:    ".hive",
		Queen: config.QueenConfig{
			MaxIterations: 10,
			ReviewTimeout: 30 * time.Second,
		},
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

	sessionID := "review-test-session"
	db.CreateSession(context.Background(), sessionID, "test objective")

	q := &Queen{
		cfg:         cfg,
		bus:         msgBus,
		db:          db,
		board:       board,
		tasks:       tasks,
		pool:        pool,
		phase:       PhasePlan,
		sessionID:   sessionID,
		assignments: make(map[string]string),
		llm:         mockClient,
	}

	return q
}

// ---------------------------------------------------------------------------
// approve_task additional tests
// ---------------------------------------------------------------------------

func TestApproveTask_MissingTaskID(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{}))
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected task_id required error, got: %v", err)
	}
}

func TestApproveTask_EmptyFeedback(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusComplete})

	result, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": "",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "approved") {
		t.Errorf("expected 'approved' in result: %s", result)
	}
}

func TestApproveTask_WithoutFeedback(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusComplete})

	result, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id": "t1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "approved") {
		t.Errorf("expected 'approved' in result: %s", result)
	}

	// Should not post to blackboard when feedback is empty/missing
	if _, ok := q.board.Read("approval-t1"); ok {
		t.Error("should not post empty feedback to blackboard")
	}
}

func TestApproveTask_PostsFeedbackToBlackboard(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusComplete})

	feedback := "Great work on the implementation!"
	_, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": feedback,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, ok := q.board.Read("approval-t1")
	if !ok {
		t.Fatal("expected approval entry on blackboard")
	}
	if entry.Value != feedback {
		t.Errorf("expected feedback %q, got %v", feedback, entry.Value)
	}
	if entry.PostedBy != "queen" {
		t.Errorf("expected PostedBy 'queen', got %v", entry.PostedBy)
	}
	if entry.TaskID != "t1" {
		t.Errorf("expected TaskID 't1', got %v", entry.TaskID)
	}
}

// ---------------------------------------------------------------------------
// reject_task additional tests
// ---------------------------------------------------------------------------

func TestRejectTask_MissingTaskID(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"feedback": "some feedback",
	}))
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected task_id required error, got: %v", err)
	}
}

func TestRejectTask_NotFound(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "nonexistent",
		"feedback": "missing",
	}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestRejectTask_AppendsFeedbackToDescription(t *testing.T) {
	q, _ := testQueen(t)
	originalDesc := "Original task description"
	q.tasks.Add(&task.Task{
		ID:          "t1",
		Title:       "Task 1",
		Type:        task.TypeCode,
		Status:      task.StatusComplete,
		MaxRetries:  3,
		RetryCount:  0,
		Description: originalDesc,
	})

	feedback := "Please add unit tests for the new function"
	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": feedback,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t1, _ := q.tasks.Get("t1")
	if !strings.Contains(t1.Description, originalDesc) {
		t.Error("original description should be preserved")
	}
	if !strings.Contains(t1.Description, "REJECTED") {
		t.Error("rejection marker should be in description")
	}
	if !strings.Contains(t1.Description, feedback) {
		t.Error("feedback should be appended to description")
	}
}

func TestRejectTask_PostsRejectionToBlackboard(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{
		ID:          "t1",
		Title:       "Task 1",
		Type:        task.TypeCode,
		Status:      task.StatusComplete,
		MaxRetries:  3,
		RetryCount:  0,
		Description: "Original",
	})

	feedback := "Missing error handling"
	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": feedback,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, ok := q.board.Read("rejection-t1-1")
	if !ok {
		t.Fatal("expected rejection entry on blackboard")
	}
	if entry.Value != feedback {
		t.Errorf("expected feedback %q, got %v", feedback, entry.Value)
	}
	if entry.TaskID != "t1" {
		t.Errorf("expected TaskID 't1', got %v", entry.TaskID)
	}
}

func TestRejectTask_IncrementsRetryCount(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{
		ID:          "t1",
		Title:       "Task 1",
		Type:        task.TypeCode,
		Status:      task.StatusComplete,
		MaxRetries:  3,
		RetryCount:  1,
		Description: "Original",
	})

	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": "Try again",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t1, _ := q.tasks.Get("t1")
	if t1.RetryCount != 2 {
		t.Errorf("expected RetryCount=2, got %d", t1.RetryCount)
	}
	if t1.Status != task.StatusPending {
		t.Errorf("expected Status=Pending, got %s", t1.Status)
	}
}

// ---------------------------------------------------------------------------
// reviewWithLLM integration tests
// ---------------------------------------------------------------------------

func TestReviewWithLLM_AutoApproveWhenNoLLM(t *testing.T) {
	q, _ := testQueen(t)
	q.llm = nil // No LLM configured

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Test Task",
		Description: "Do something",
	}
	result := &task.Result{Success: true, Output: "done"}

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verdict.Approved {
		t.Error("expected auto-approve when no LLM configured")
	}
	if verdict.Reason != "LLM review not configured, auto-approved" {
		t.Errorf("unexpected reason: %s", verdict.Reason)
	}
}

func TestReviewWithLLM_ApprovedVerdict(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{"approved": true, "reason": "Task completed correctly with proper implementation", "suggestions": []}`,
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Implement feature X",
		Description: "Write a function that does X",
		Constraints: []string{"Use existing patterns"},
	}
	result := &task.Result{
		Success: true,
		Output:  "Implemented feature X in file.go",
	}

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verdict.Approved {
		t.Errorf("expected approved=true, got %v", verdict.Approved)
	}
	if !strings.Contains(verdict.Reason, "correctly") {
		t.Errorf("unexpected reason: %s", verdict.Reason)
	}
}

func TestReviewWithLLM_RejectedVerdict(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{"approved": false, "reason": "Output does not match requirements", "suggestions": ["Add proper error handling", "Write unit tests"]}`,
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Implement feature X",
		Description: "Write a function with error handling",
	}
	result := &task.Result{
		Success: true,
		Output:  "Implemented without error handling",
	}

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Approved {
		t.Error("expected approved=false")
	}
	if !strings.Contains(verdict.Reason, "does not match") {
		t.Errorf("unexpected reason: %s", verdict.Reason)
	}
	if len(verdict.Suggestions) != 2 {
		t.Errorf("expected 2 suggestions, got %d", len(verdict.Suggestions))
	}
}

func TestReviewWithLLM_WithNewTasks(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{
			"approved": true,
			"reason": "Implementation is correct",
			"suggestions": [],
			"new_tasks": [
				{"type": "test", "title": "Add tests", "description": "Write unit tests for the new code", "depends_on": ["t1"]},
				{"type": "review", "title": "Code review", "description": "Review the implementation", "depends_on": ["t1", "t2"]}
			]
		}`,
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Implement feature",
		Description: "Write the feature",
	}
	result := &task.Result{Success: true, Output: "done"}

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(verdict.NewTasks) != 2 {
		t.Fatalf("expected 2 new tasks, got %d", len(verdict.NewTasks))
	}

	nt0 := verdict.NewTasks[0]
	if nt0.Type != "test" {
		t.Errorf("expected type 'test', got %s", nt0.Type)
	}
	if nt0.Title != "Add tests" {
		t.Errorf("expected title 'Add tests', got %s", nt0.Title)
	}
	if len(nt0.DependsOn) != 1 || nt0.DependsOn[0] != "t1" {
		t.Errorf("unexpected depends_on: %v", nt0.DependsOn)
	}

	nt1 := verdict.NewTasks[1]
	if nt1.Type != "review" {
		t.Errorf("expected type 'review', got %s", nt1.Type)
	}
	if len(nt1.DependsOn) != 2 {
		t.Errorf("expected 2 depends_on, got %d", len(nt1.DependsOn))
	}
}

func TestReviewWithLLM_WithErrors(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{"approved": false, "reason": "Worker produced errors", "suggestions": ["Fix compilation errors"]}`,
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Implement feature",
		Description: "Write the feature",
	}
	result := &task.Result{
		Success: false,
		Output:  "Partial output",
		Errors:  []string{"compile error: undefined variable 'x'", "lint warning: unused import"},
	}

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Approved {
		t.Error("expected approved=false for failed result")
	}
}

func TestReviewWithLLM_LLMError(t *testing.T) {
	mockClient := &mockReviewClient{
		err: context.DeadlineExceeded,
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Test",
		Description: "Test task",
	}
	result := &task.Result{Success: true, Output: "done"}

	_, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err == nil {
		t.Fatal("expected error from LLM call")
	}
}

func TestReviewWithLLM_ParseError(t *testing.T) {
	mockClient := &mockReviewClient{
		response: "This is not JSON",
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Test",
		Description: "Test task",
	}
	result := &task.Result{Success: true, Output: "done"}

	_, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestReviewWithLLM_MalformedJSON(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{"approved": true, "reason": "ok"`, // missing closing brace
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Test",
		Description: "Test task",
	}
	result := &task.Result{Success: true, Output: "done"}

	_, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err == nil {
		t.Fatal("expected parse error for malformed JSON")
	}
}

func TestReviewWithLLM_WithMarkdownFences(t *testing.T) {
	mockClient := &mockReviewClient{
		response: "```json\n{\"approved\": true, \"reason\": \"Looks good\", \"suggestions\": []}\n```",
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Test",
		Description: "Test task",
	}
	result := &task.Result{Success: true, Output: "done"}

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verdict.Approved {
		t.Error("expected approved=true")
	}
}

func TestReviewWithLLM_TruncatesLongOutput(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{"approved": true, "reason": "Good", "suggestions": []}`,
	}
	q := reviewTestQueen(t, mockClient)

	longOutput := strings.Repeat("x", maxOutputChars+1000)
	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Test",
		Description: "Test task",
	}
	result := &task.Result{Success: true, Output: longOutput}

	_, err := q.reviewWithLLM(context.Background(), "t1", tk, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Integration tests: approve/reject with LLM review
// ---------------------------------------------------------------------------

func TestIntegration_ReviewAndApprove(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{"approved": true, "reason": "Implementation is correct and complete"}`,
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Implement login",
		Description: "Add login functionality",
		Status:      task.StatusComplete,
		Result:      &task.Result{Success: true, Output: "Added login.go with auth"},
	}
	q.tasks.Add(tk)

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, tk.Result)
	if err != nil {
		t.Fatalf("review error: %v", err)
	}

	if !verdict.Approved {
		t.Fatal("expected approval")
	}

	result, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": "LGTM!",
	}))
	if err != nil {
		t.Fatalf("approve error: %v", err)
	}
	if !strings.Contains(result, "approved") {
		t.Errorf("unexpected result: %s", result)
	}

	entry, ok := q.board.Read("approval-t1")
	if !ok {
		t.Fatal("expected approval on blackboard")
	}
	if entry.Value != "LGTM!" {
		t.Errorf("unexpected feedback: %v", entry.Value)
	}
}

func TestIntegration_ReviewAndReject(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{"approved": false, "reason": "Missing required error handling", "suggestions": ["Add try-catch blocks"]}`,
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Implement feature",
		Description: "Write the feature",
		Status:      task.StatusComplete,
		MaxRetries:  3,
		Result:      &task.Result{Success: true, Output: "Partial implementation"},
	}
	q.tasks.Add(tk)

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, tk.Result)
	if err != nil {
		t.Fatalf("review error: %v", err)
	}

	if verdict.Approved {
		t.Fatal("expected rejection")
	}

	result, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": verdict.Reason,
	}))
	if err != nil {
		t.Fatalf("reject error: %v", err)
	}
	if !strings.Contains(result, "rejected") {
		t.Errorf("unexpected result: %s", result)
	}

	t1, _ := q.tasks.Get("t1")
	if t1.Status != task.StatusPending {
		t.Errorf("expected status Pending, got %s", t1.Status)
	}
	if t1.RetryCount != 1 {
		t.Errorf("expected retry count 1, got %d", t1.RetryCount)
	}
}

func TestIntegration_ReviewWithNewTasks(t *testing.T) {
	mockClient := &mockReviewClient{
		response: `{
			"approved": true,
			"reason": "Implementation complete",
			"suggestions": [],
			"new_tasks": [
				{"type": "test", "title": "Test the feature", "description": "Write tests", "depends_on": ["t1"]}
			]
		}`,
	}
	q := reviewTestQueen(t, mockClient)

	tk := &task.Task{
		ID:          "t1",
		Type:        task.TypeCode,
		Title:       "Implement feature",
		Description: "Write the feature",
		Status:      task.StatusComplete,
		Result:      &task.Result{Success: true, Output: "done"},
	}
	q.tasks.Add(tk)

	verdict, err := q.reviewWithLLM(context.Background(), "t1", tk, tk.Result)
	if err != nil {
		t.Fatalf("review error: %v", err)
	}

	if len(verdict.NewTasks) != 1 {
		t.Fatalf("expected 1 new task, got %d", len(verdict.NewTasks))
	}

	newTask := verdict.NewTasks[0]
	if newTask.Title != "Test the feature" {
		t.Errorf("unexpected title: %s", newTask.Title)
	}
	if newTask.Type != "test" {
		t.Errorf("unexpected type: %s", newTask.Type)
	}
	if len(newTask.DependsOn) != 1 || newTask.DependsOn[0] != "t1" {
		t.Errorf("unexpected depends_on: %v", newTask.DependsOn)
	}
}
