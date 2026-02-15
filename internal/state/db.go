package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// DB is a SQLite-backed state store replacing the JSONL + JSON files
type DB struct {
	mu   sync.Mutex
	db   *sql.DB
	path string
}

// OpenDB opens (or creates) the hive SQLite database
func OpenDB(hiveDir string) (*DB, error) {
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		return nil, fmt.Errorf("create hive dir: %w", err)
	}

	dbPath := filepath.Join(hiveDir, "hive.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Single connection for writes, WAL allows concurrent reads
	db.SetMaxOpenConns(2)

	s := &DB{db: db, path: dbPath}
	if err := s.migrate(); err != nil {
		db.Close()
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
	`
	_, err := s.db.Exec(ddl)
	return err
}

// --- Session operations ---

// SessionInfoFull includes phase and iteration for resumption.
type SessionInfoFull struct {
	SessionInfo
	Phase     string `json:"phase"`
	Iteration int    `json:"iteration"`
}

// GetSessionFull retrieves session info including phase and iteration.
func (s *DB) GetSessionFull(id string) (*SessionInfoFull, error) {
	row := s.db.QueryRow(`SELECT id, objective, status, created_at, updated_at, COALESCE(phase, 'plan'), COALESCE(iteration, 0) FROM sessions WHERE id = ?`, id)
	var si SessionInfoFull
	if err := row.Scan(&si.ID, &si.Objective, &si.Status, &si.CreatedAt, &si.UpdatedAt, &si.Phase, &si.Iteration); err != nil {
		return nil, err
	}
	return &si, nil
}

func (s *DB) CreateSession(id, objective string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, objective, status, created_at, updated_at) VALUES (?, ?, 'running', ?, ?)`,
		id, objective, now, now,
	)
	return err
}

func (s *DB) UpdateSessionStatus(id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`,
		status, now, id,
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

func (s *DB) GetSession(id string) (*SessionInfo, error) {
	row := s.db.QueryRow(`SELECT id, objective, status, created_at, updated_at FROM sessions WHERE id = ?`, id)
	var si SessionInfo
	if err := row.Scan(&si.ID, &si.Objective, &si.Status, &si.CreatedAt, &si.UpdatedAt); err != nil {
		return nil, err
	}
	return &si, nil
}

func (s *DB) LatestSession() (*SessionInfo, error) {
	row := s.db.QueryRow(`SELECT id, objective, status, created_at, updated_at FROM sessions ORDER BY created_at DESC LIMIT 1`)
	var si SessionInfo
	if err := row.Scan(&si.ID, &si.Objective, &si.Status, &si.CreatedAt, &si.UpdatedAt); err != nil {
		return nil, err
	}
	return &si, nil
}

// FindResumableSession returns the most recent session that is not 'done' and can be resumed.
// It excludes sessions with status 'done' or 'cancelled'.
func (s *DB) FindResumableSession() (*SessionInfo, error) {
	row := s.db.QueryRow(`
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
func (s *DB) UpdateSessionPhase(sessionID string, phase string, iteration int) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`UPDATE sessions SET phase = ?, iteration = ?, updated_at = ? WHERE id = ?`,
		phase, iteration, now, sessionID,
	)
	return err
}

// GetSessionPhase retrieves the saved phase and iteration for a session.
func (s *DB) GetSessionPhase(sessionID string) (string, int, error) {
	var phase string
	var iteration int
	err := s.db.QueryRow(
		`SELECT COALESCE(phase, 'plan'), COALESCE(iteration, 0) FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&phase, &iteration)
	return phase, iteration, err
}

// --- Event log (append-only) ---

func (s *DB) AppendEvent(sessionID, eventType string, data interface{}) (int64, error) {
	var dataStr string
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return 0, err
		}
		dataStr = string(b)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.Exec(
		`INSERT INTO events (session_id, type, data, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, eventType, dataStr, now,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *DB) EventCount(sessionID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}

// --- Task operations ---

type TaskRow struct {
	ID          string  `json:"id"`
	SessionID   string  `json:"session_id"`
	Type        string  `json:"type"`
	Status      string  `json:"status"`
	Priority    int     `json:"priority"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	WorkerID    *string `json:"worker_id,omitempty"`
	Result      *string `json:"result,omitempty"`
	ResultData  *string `json:"result_data,omitempty"`
	MaxRetries  int     `json:"max_retries"`
	RetryCount  int     `json:"retry_count"`
	DependsOn   string  `json:"depends_on"`
	CreatedAt   string  `json:"created_at"`
	StartedAt   *string `json:"started_at,omitempty"`
	CompletedAt *string `json:"completed_at,omitempty"`
}

func (s *DB) InsertTask(sessionID string, t TaskRow) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO tasks
		(id, session_id, type, status, priority, title, description, max_retries, retry_count, depends_on, timeout_ns, created_at, result_data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, sessionID, t.Type, t.Status, t.Priority, t.Title, t.Description,
		t.MaxRetries, t.RetryCount, t.DependsOn, 0, now, t.ResultData,
	)
	return err
}

func (s *DB) UpdateTaskStatus(sessionID, taskID, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var col string
	switch status {
	case "running":
		col = "started_at"
	case "complete", "failed", "cancelled":
		col = "completed_at"
	}
	if col != "" {
		_, err := s.db.Exec(
			fmt.Sprintf(`UPDATE tasks SET status = ?, %s = ? WHERE id = ? AND session_id = ?`, col),
			status, now, taskID, sessionID,
		)
		return err
	}
	_, err := s.db.Exec(
		`UPDATE tasks SET status = ? WHERE id = ? AND session_id = ?`,
		status, taskID, sessionID,
	)
	return err
}

func (s *DB) UpdateTaskWorker(sessionID, taskID, workerID string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET worker_id = ? WHERE id = ? AND session_id = ?`,
		workerID, taskID, sessionID,
	)
	return err
}

func (s *DB) UpdateTaskResult(sessionID, taskID string, result interface{}) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE tasks SET result = ?, result_data = ? WHERE id = ? AND session_id = ?`,
		string(b), string(b), taskID, sessionID,
	)
	return err
}

// UpdateTaskRetryCount sets the retry count for a task.
func (s *DB) UpdateTaskRetryCount(sessionID, taskID string, retryCount int) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET retry_count = ? WHERE id = ? AND session_id = ?`,
		retryCount, taskID, sessionID,
	)
	return err
}

// UpdateTaskErrorType sets the last error type for a task.
func (s *DB) UpdateTaskErrorType(sessionID, taskID, errorType string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after commit

	_, err = tx.Exec(
		`UPDATE tasks SET result_data = COALESCE(result_data, '{}') WHERE id = ? AND session_id = ?`,
		taskID, sessionID,
	)
	if err != nil {
		return err
	}
	// Store error type in a separate column by using a JSON patch approach
	// For now, we'll store it in the result_data field as JSON
	_, err = tx.Exec(
		`UPDATE tasks SET result_data = json_set(COALESCE(result_data, '{}'), '$.last_error_type', ?) WHERE id = ? AND session_id = ?`,
		errorType, taskID, sessionID,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *DB) IncrementTaskRetry(sessionID, taskID string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after commit

	_, err = tx.Exec(
		`UPDATE tasks SET retry_count = retry_count + 1 WHERE id = ? AND session_id = ?`,
		taskID, sessionID,
	)
	if err != nil {
		return 0, err
	}
	var count int
	err = tx.QueryRow(`SELECT retry_count FROM tasks WHERE id = ? AND session_id = ?`, taskID, sessionID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func (s *DB) GetTask(sessionID, taskID string) (*TaskRow, error) {
	row := s.db.QueryRow(
		`SELECT id, session_id, type, status, priority, title, description, worker_id, result,
		 max_retries, retry_count, depends_on, created_at, started_at, completed_at, result_data
		 FROM tasks WHERE id = ? AND session_id = ?`,
		taskID, sessionID,
	)
	return scanTask(row)
}

func (s *DB) GetTasks(sessionID string) ([]TaskRow, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, type, status, priority, title, description, worker_id, result,
		 max_retries, retry_count, depends_on, created_at, started_at, completed_at, result_data
		 FROM tasks WHERE session_id = ? ORDER BY priority DESC, created_at`,
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

func (s *DB) GetTasksByStatus(sessionID, status string) ([]TaskRow, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, type, status, priority, title, description, worker_id, result,
		 max_retries, retry_count, depends_on, created_at, started_at, completed_at, result_data
		 FROM tasks WHERE session_id = ? AND status = ? ORDER BY priority DESC`,
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

func (s *DB) CountTasksByStatus(sessionID string) (map[string]int, error) {
	rows, err := s.db.Query(
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

func (s *DB) PostBlackboard(sessionID, key, value, postedBy, taskID, tags string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO blackboard (key, session_id, value, posted_by, task_id, tags, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key, sessionID, value, postedBy, taskID, tags, now,
	)
	return err
}

func (s *DB) ReadBlackboard(sessionID, key string) (string, error) {
	var value string
	err := s.db.QueryRow(
		`SELECT value FROM blackboard WHERE key = ? AND session_id = ?`,
		key, sessionID,
	).Scan(&value)
	return value, err
}

// --- KV (general purpose) ---

func (s *DB) SetKV(key, value string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)`, key, value)
	return err
}

func (s *DB) GetKV(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	return value, err
}

// --- Lifecycle ---

func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) Raw() *sql.DB {
	return s.db
}

// --- scan helpers ---

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanTask(row scannable) (*TaskRow, error) {
	var t TaskRow
	err := row.Scan(
		&t.ID, &t.SessionID, &t.Type, &t.Status, &t.Priority,
		&t.Title, &t.Description, &t.WorkerID, &t.Result,
		&t.MaxRetries, &t.RetryCount, &t.DependsOn,
		&t.CreatedAt, &t.StartedAt, &t.CompletedAt, &t.ResultData,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func scanTaskRows(rows *sql.Rows) (*TaskRow, error) {
	return scanTask(rows)
}

// ResetRunningTasks marks all 'running' tasks as 'pending' for session resumption.
// This should be called when resuming a session to retry tasks that were interrupted.
func (s *DB) ResetRunningTasks(sessionID string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET status = 'pending', worker_id = NULL, started_at = NULL WHERE session_id = ? AND status = 'running'`,
		sessionID,
	)
	return err
}
