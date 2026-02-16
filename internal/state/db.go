package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB is a SQLite-backed state store replacing the JSONL + JSON files
type DB struct {
	writer *sql.DB
	reader *sql.DB
	path   string
}

// OpenDB opens (or creates) the hive SQLite database
func OpenDB(hiveDir string) (*DB, error) {
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		return nil, fmt.Errorf("create hive dir: %w", err)
	}

	dbPath := filepath.Join(hiveDir, "hive.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL", dbPath)

	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open writer db: %w", err)
	}
	writer.SetMaxOpenConns(1)
	// Ensure WAL mode and busy timeout are set on the writer connection
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := writer.Exec(pragma); err != nil {
			writer.Close()
			return nil, fmt.Errorf("writer %s: %w", pragma, err)
		}
	}

	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open reader db: %w", err)
	}
	reader.SetMaxOpenConns(3)
	// Ensure WAL mode and busy timeout are set on reader connections
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA query_only=1",
	} {
		if _, err := reader.Exec(pragma); err != nil {
			writer.Close()
			reader.Close()
			return nil, fmt.Errorf("reader %s: %w", pragma, err)
		}
	}

	s := &DB{writer: writer, reader: reader, path: dbPath}
	if err := s.migrate(); err != nil {
		writer.Close()
		reader.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *DB) migrate() error {
	ddl := `
	CREATE TABLE IF NOT EXISTS sessions (
		id          TEXT PRIMARY KEY,
		objective   TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'running',
		phase       TEXT DEFAULT 'plan',
		iteration   INTEGER DEFAULT 0,
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS events (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id  TEXT NOT NULL,
		type        TEXT NOT NULL,
		data        TEXT,
		created_at  TEXT NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);
	CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id);
	CREATE INDEX IF NOT EXISTS idx_events_type ON events(type);

	CREATE TABLE IF NOT EXISTS tasks (
		id          TEXT NOT NULL,
		session_id  TEXT NOT NULL,
		parent_id   TEXT,
		type        TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'pending',
		priority    INTEGER NOT NULL DEFAULT 1,
		title       TEXT NOT NULL,
		description TEXT,
		context     TEXT,
		worker_id   TEXT,
		result      TEXT,
		max_retries INTEGER NOT NULL DEFAULT 2,
		retry_count INTEGER NOT NULL DEFAULT 0,
		result_data TEXT,
		depends_on  TEXT,
		timeout_ns  INTEGER,
		created_at  TEXT NOT NULL,
		started_at  TEXT,
		completed_at TEXT,
		PRIMARY KEY (id, session_id),
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);
	CREATE INDEX IF NOT EXISTS idx_tasks_session ON tasks(session_id);
	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(session_id, status);

	CREATE TABLE IF NOT EXISTS blackboard (
		key         TEXT NOT NULL,
		session_id  TEXT NOT NULL,
		value       TEXT,
		posted_by   TEXT,
		task_id     TEXT,
		tags        TEXT,
		created_at  TEXT NOT NULL,
		PRIMARY KEY (key, session_id),
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	CREATE TABLE IF NOT EXISTS kv (
		key   TEXT PRIMARY KEY,
		value TEXT
	);

	CREATE TABLE IF NOT EXISTS messages (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id  TEXT NOT NULL,
		sequence_id INTEGER NOT NULL,
		role        TEXT NOT NULL,
		content     TEXT NOT NULL,
		usage_data  TEXT,
		excluded    INTEGER DEFAULT 0,
		created_at  TEXT NOT NULL,
		UNIQUE(session_id, sequence_id),
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);
	CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
	`
	_, err := s.writer.Exec(ddl)
	if err != nil {
		return err
	}

	// Add columns for task constraints/context/allowed_paths (idempotent).
	for _, col := range []string{
		"ALTER TABLE tasks ADD COLUMN constraints TEXT",
		"ALTER TABLE tasks ADD COLUMN allowed_paths TEXT",
	} {
		_, _ = s.writer.Exec(col) // ignore "duplicate column" errors
	}

	return nil
}

// --- Session operations ---

// SessionInfoFull includes phase and iteration for resumption.
type SessionInfoFull struct {
	SessionInfo
	Phase     string `json:"phase"`
	Iteration int    `json:"iteration"`
}

// GetSessionFull retrieves session info including phase and iteration.
func (s *DB) GetSessionFull(ctx context.Context, id string) (*SessionInfoFull, error) {
	row := s.reader.QueryRowContext(ctx, `SELECT id, objective, status, created_at, updated_at, COALESCE(phase, 'plan'), COALESCE(iteration, 0) FROM sessions WHERE id = ?`, id)
	var si SessionInfoFull
	if err := row.Scan(&si.ID, &si.Objective, &si.Status, &si.CreatedAt, &si.UpdatedAt, &si.Phase, &si.Iteration); err != nil {
		return nil, err
	}
	return &si, nil
}

func (s *DB) CreateSession(ctx context.Context, id, objective string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO sessions (id, objective, status, created_at, updated_at) VALUES (?, ?, 'running', ?, ?)`,
		id, objective, now, now,
	)
	return err
}

func (s *DB) UpdateSessionStatus(ctx context.Context, id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writer.ExecContext(ctx,
		`UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`,
		status, now, id,
	)
	return err
}

func (s *DB) StopSession(id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writer.Exec(
		`UPDATE sessions SET status = 'stopped', updated_at = ? WHERE id = ?`,
		now, id,
	)
	return err
}

type SessionInfo struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (s *DB) GetSession(ctx context.Context, id string) (*SessionInfo, error) {
	row := s.reader.QueryRowContext(ctx, `SELECT id, objective, status, created_at, updated_at FROM sessions WHERE id = ?`, id)
	var si SessionInfo
	if err := row.Scan(&si.ID, &si.Objective, &si.Status, &si.CreatedAt, &si.UpdatedAt); err != nil {
		return nil, err
	}
	return &si, nil
}

func (s *DB) LatestSession(ctx context.Context) (*SessionInfo, error) {
	row := s.reader.QueryRowContext(ctx, `SELECT id, objective, status, created_at, updated_at FROM sessions ORDER BY created_at DESC LIMIT 1`)
	var si SessionInfo
	if err := row.Scan(&si.ID, &si.Objective, &si.Status, &si.CreatedAt, &si.UpdatedAt); err != nil {
		return nil, err
	}
	return &si, nil
}

// FindResumableSession returns the most recent session that is not 'done' and can be resumed.
// It excludes sessions with status 'done' or 'cancelled'.
func (s *DB) FindResumableSession(ctx context.Context) (*SessionInfo, error) {
	row := s.reader.QueryRowContext(ctx, `
		SELECT id, objective, status, created_at, updated_at
		FROM sessions
		WHERE status NOT IN ('done', 'cancelled')
		ORDER BY created_at DESC LIMIT 1`)
	var si SessionInfo
	if err := row.Scan(&si.ID, &si.Objective, &si.Status, &si.CreatedAt, &si.UpdatedAt); err != nil {
		return nil, err
	}
	return &si, nil
}

// UpdateSessionPhase saves the current phase and iteration for session resumption.
func (s *DB) UpdateSessionPhase(ctx context.Context, sessionID string, phase string, iteration int) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writer.ExecContext(ctx,
		`UPDATE sessions SET phase = ?, iteration = ?, updated_at = ? WHERE id = ?`,
		phase, iteration, now, sessionID,
	)
	return err
}

// GetSessionPhase retrieves the saved phase and iteration for a session.
func (s *DB) GetSessionPhase(ctx context.Context, sessionID string) (string, int, error) {
	var phase string
	var iteration int
	err := s.reader.QueryRowContext(ctx,
		`SELECT COALESCE(phase, 'plan'), COALESCE(iteration, 0) FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&phase, &iteration)
	return phase, iteration, err
}

// --- Event log (append-only) ---

// EventRow represents a row from the events table.
type EventRow struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Data      string `json:"data"`
	CreatedAt string `json:"created_at"`
}

// ListEvents returns events for a session, ordered by creation time.
// If afterID > 0, only returns events with id > afterID (for tailing).
func (s *DB) ListEvents(ctx context.Context, sessionID string, limit int, afterID int64) ([]EventRow, error) {
	query := `SELECT id, session_id, type, COALESCE(data, ''), created_at FROM events
	          WHERE session_id = ? AND id > ? ORDER BY id ASC LIMIT ?`
	rows, err := s.reader.QueryContext(ctx, query, sessionID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Type, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *DB) AppendEvent(ctx context.Context, sessionID, eventType string, data interface{}) (int64, error) {
	var dataStr string
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return 0, err
		}
		dataStr = string(b)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.writer.ExecContext(ctx,
		`INSERT INTO events (session_id, type, data, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, eventType, dataStr, now,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *DB) EventCount(ctx context.Context, sessionID string) (int, error) {
	var count int
	err := s.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}

// --- Task operations ---

type TaskRow struct {
	ID           string  `json:"id"`
	SessionID    string  `json:"session_id"`
	Type         string  `json:"type"`
	Status       string  `json:"status"`
	Priority     int     `json:"priority"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Constraints  string  `json:"constraints,omitempty"`   // JSON array of strings
	Context      string  `json:"context,omitempty"`       // JSON object of key-value pairs
	AllowedPaths string  `json:"allowed_paths,omitempty"` // JSON array of strings
	WorkerID     *string `json:"worker_id,omitempty"`
	Result       *string `json:"result,omitempty"`
	ResultData   *string `json:"result_data,omitempty"`
	MaxRetries   int     `json:"max_retries"`
	RetryCount   int     `json:"retry_count"`
	DependsOn    string  `json:"depends_on"`
	CreatedAt    string  `json:"created_at"`
	StartedAt    *string `json:"started_at,omitempty"`
	CompletedAt  *string `json:"completed_at,omitempty"`
}

func (s *DB) InsertTask(ctx context.Context, sessionID string, t TaskRow) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writer.ExecContext(ctx,
		`INSERT OR REPLACE INTO tasks
		(id, session_id, type, status, priority, title, description, constraints, allowed_paths, context, max_retries, retry_count, depends_on, timeout_ns, created_at, result_data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, sessionID, t.Type, t.Status, t.Priority, t.Title, t.Description,
		nilIfEmpty(t.Constraints), nilIfEmpty(t.AllowedPaths), nilIfEmpty(t.Context),
		t.MaxRetries, t.RetryCount, t.DependsOn, 0, now, t.ResultData,
	)
	return err
}

// nilIfEmpty returns nil for empty strings so SQLite stores NULL.
func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (s *DB) UpdateTaskStatus(ctx context.Context, sessionID, taskID, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var col string
	switch status {
	case "running":
		col = "started_at"
	case "complete", "failed", "cancelled":
		col = "completed_at"
	}
	if col != "" {
		_, err := s.writer.ExecContext(ctx,
			fmt.Sprintf(`UPDATE tasks SET status = ?, %s = ? WHERE id = ? AND session_id = ?`, col),
			status, now, taskID, sessionID,
		)
		return err
	}
	_, err := s.writer.ExecContext(ctx,
		`UPDATE tasks SET status = ? WHERE id = ? AND session_id = ?`,
		status, taskID, sessionID,
	)
	return err
}

func (s *DB) UpdateTaskWorker(ctx context.Context, sessionID, taskID, workerID string) error {
	_, err := s.writer.ExecContext(ctx,
		`UPDATE tasks SET worker_id = ? WHERE id = ? AND session_id = ?`,
		workerID, taskID, sessionID,
	)
	return err
}

func (s *DB) UpdateTaskResult(ctx context.Context, sessionID, taskID string, result interface{}) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.writer.ExecContext(ctx,
		`UPDATE tasks SET result = ?, result_data = ? WHERE id = ? AND session_id = ?`,
		string(b), string(b), taskID, sessionID,
	)
	return err
}

// UpdateTaskRetryCount sets the retry count for a task.
func (s *DB) UpdateTaskRetryCount(ctx context.Context, sessionID, taskID string, retryCount int) error {
	_, err := s.writer.ExecContext(ctx,
		`UPDATE tasks SET retry_count = ? WHERE id = ? AND session_id = ?`,
		retryCount, taskID, sessionID,
	)
	return err
}

// UpdateTaskErrorType sets the last error type for a task.
func (s *DB) UpdateTaskErrorType(ctx context.Context, sessionID, taskID, errorType string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	_, err = tx.ExecContext(ctx,
		`UPDATE tasks SET result_data = COALESCE(result_data, '{}') WHERE id = ? AND session_id = ?`,
		taskID, sessionID,
	)
	if err != nil {
		return err
	}
	// Store error type in a separate column by using a JSON patch approach
	// For now, we'll store it in the result_data field as JSON
	_, err = tx.ExecContext(ctx,
		`UPDATE tasks SET result_data = json_set(COALESCE(result_data, '{}'), '$.last_error_type', ?) WHERE id = ? AND session_id = ?`,
		errorType, taskID, sessionID,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DB) IncrementTaskRetry(ctx context.Context, sessionID, taskID string) (int, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	_, err = tx.ExecContext(ctx,
		`UPDATE tasks SET retry_count = retry_count + 1 WHERE id = ? AND session_id = ?`,
		taskID, sessionID,
	)
	if err != nil {
		return 0, err
	}
	var count int
	err = tx.QueryRowContext(ctx, `SELECT retry_count FROM tasks WHERE id = ? AND session_id = ?`, taskID, sessionID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

// taskSelectCols is the column list for all task SELECT queries.
const taskSelectCols = `id, session_id, type, status, priority, title, description,
	constraints, context, allowed_paths,
	worker_id, result, max_retries, retry_count, depends_on,
	created_at, started_at, completed_at, result_data`

func (s *DB) GetTask(ctx context.Context, sessionID, taskID string) (*TaskRow, error) {
	row := s.reader.QueryRowContext(ctx,
		`SELECT `+taskSelectCols+` FROM tasks WHERE id = ? AND session_id = ?`,
		taskID, sessionID,
	)
	return scanTask(row)
}

func (s *DB) GetTasks(ctx context.Context, sessionID string) ([]TaskRow, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT `+taskSelectCols+` FROM tasks WHERE session_id = ? ORDER BY priority DESC, created_at`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []TaskRow
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func (s *DB) GetTasksByStatus(ctx context.Context, sessionID, status string) ([]TaskRow, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT `+taskSelectCols+` FROM tasks WHERE session_id = ? AND status = ? ORDER BY priority DESC`,
		sessionID, status,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []TaskRow
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func (s *DB) CountTasksByStatus(ctx context.Context, sessionID string) (map[string]int, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM tasks WHERE session_id = ? GROUP BY status`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var st string
		var c int
		if err := rows.Scan(&st, &c); err != nil {
			return nil, err
		}
		counts[st] = c
	}
	return counts, rows.Err()
}

// --- Blackboard operations ---

func (s *DB) PostBlackboard(ctx context.Context, sessionID, key, value, postedBy, taskID, tags string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writer.ExecContext(ctx,
		`INSERT OR REPLACE INTO blackboard (key, session_id, value, posted_by, task_id, tags, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key, sessionID, value, postedBy, taskID, tags, now,
	)
	return err
}

func (s *DB) ReadBlackboard(ctx context.Context, sessionID, key string) (string, error) {
	var value string
	err := s.reader.QueryRowContext(ctx,
		`SELECT value FROM blackboard WHERE key = ? AND session_id = ?`,
		key, sessionID,
	).Scan(&value)
	return value, err
}

// --- KV (general purpose) ---

func (s *DB) SetKV(ctx context.Context, key, value string) error {
	_, err := s.writer.ExecContext(ctx, `INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)`, key, value)
	return err
}

func (s *DB) GetKV(ctx context.Context, key string) (string, error) {
	var value string
	err := s.reader.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	return value, err
}

// --- Message operations ---

// MessageRow represents a stored message.
type MessageRow struct {
	SequenceID int
	Role       string
	Content    string
	UsageData  string
}

// AppendMessage appends a message to a session's conversation history.
func (s *DB) AppendMessage(ctx context.Context, sessionID string, sequenceID int, role string, content string, usageData string) error {
	_, err := s.writer.ExecContext(ctx, `
		INSERT OR REPLACE INTO messages (session_id, sequence_id, role, content, usage_data, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, sequenceID, role, content, usageData, time.Now().Format(time.RFC3339))
	return err
}

// LoadMessages loads all non-excluded messages for a session, ordered by sequence_id.
func (s *DB) LoadMessages(ctx context.Context, sessionID string) ([]MessageRow, error) {
	rows, err := s.reader.QueryContext(ctx, `
		SELECT sequence_id, role, content, usage_data
		FROM messages WHERE session_id = ? AND excluded = 0
		ORDER BY sequence_id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []MessageRow
	for rows.Next() {
		var m MessageRow
		var usageData sql.NullString
		if err := rows.Scan(&m.SequenceID, &m.Role, &m.Content, &usageData); err != nil {
			return nil, err
		}
		m.UsageData = usageData.String
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// MarkMessageExcluded marks a message as excluded from conversation loading.
func (s *DB) MarkMessageExcluded(ctx context.Context, sessionID string, sequenceID int) error {
	_, err := s.writer.ExecContext(ctx, `
		UPDATE messages SET excluded = 1 WHERE session_id = ? AND sequence_id = ?`,
		sessionID, sequenceID)
	return err
}

// --- Lifecycle ---

func (s *DB) Close() error {
	rErr := s.reader.Close()
	wErr := s.writer.Close()
	if wErr != nil {
		return wErr
	}
	return rErr
}

// --- scan helpers ---

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanTask(row scannable) (*TaskRow, error) {
	var t TaskRow
	var constraints, ctx, allowedPaths sql.NullString
	err := row.Scan(
		&t.ID, &t.SessionID, &t.Type, &t.Status, &t.Priority,
		&t.Title, &t.Description,
		&constraints, &ctx, &allowedPaths,
		&t.WorkerID, &t.Result,
		&t.MaxRetries, &t.RetryCount, &t.DependsOn,
		&t.CreatedAt, &t.StartedAt, &t.CompletedAt, &t.ResultData,
	)
	if err != nil {
		return nil, err
	}
	t.Constraints = constraints.String
	t.Context = ctx.String
	t.AllowedPaths = allowedPaths.String
	return &t, nil
}

func scanTaskRows(rows *sql.Rows) (*TaskRow, error) {
	return scanTask(rows)
}

// SessionSummary includes session info plus task counts.
type SessionSummary struct {
	ID             string `json:"id"`
	Objective      string `json:"objective"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	TotalTasks     int    `json:"total_tasks"`
	CompletedTasks int    `json:"completed_tasks"`
	FailedTasks    int    `json:"failed_tasks"`
	PendingTasks   int    `json:"pending_tasks"`
}

// ListSessions returns session summaries with task counts, ordered by most recent first.
func (s *DB) ListSessions(ctx context.Context, limit int, onlyRunning bool) ([]SessionSummary, error) {
	query := `
		SELECT s.id, s.objective, s.status, s.created_at, s.updated_at,
			COUNT(t.id) AS total_tasks,
			COALESCE(SUM(CASE WHEN t.status = 'complete' THEN 1 ELSE 0 END), 0) AS completed,
			COALESCE(SUM(CASE WHEN t.status = 'failed' THEN 1 ELSE 0 END), 0) AS failed,
			COALESCE(SUM(CASE WHEN t.status = 'pending' THEN 1 ELSE 0 END), 0) AS pending
		FROM sessions s
		LEFT JOIN tasks t ON s.id = t.session_id`
	args := []any{}
	if onlyRunning {
		query += ` WHERE s.status = ?`
		args = append(args, "running")
	}
	query += ` GROUP BY s.id ORDER BY s.created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionSummary
	for rows.Next() {
		var ss SessionSummary
		if err := rows.Scan(&ss.ID, &ss.Objective, &ss.Status, &ss.CreatedAt, &ss.UpdatedAt,
			&ss.TotalTasks, &ss.CompletedTasks, &ss.FailedTasks, &ss.PendingTasks); err != nil {
			return nil, err
		}
		sessions = append(sessions, ss)
	}
	return sessions, rows.Err()
}

// ResetRunningTasks marks all 'running' tasks as 'pending' for session resumption.
// This should be called when resuming a session to retry tasks that were interrupted.
func (s *DB) ResetRunningTasks(ctx context.Context, sessionID string) error {
	_, err := s.writer.ExecContext(ctx,
		`UPDATE tasks SET status = 'pending', worker_id = NULL, started_at = NULL WHERE session_id = ? AND status = 'running'`,
		sessionID,
	)
	return err
}
