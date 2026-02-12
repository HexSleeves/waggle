package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event represents a single entry in the append-only event log
type Event struct {
	ID        int         `json:"id"`
	Type      string      `json:"type"`
	Timestamp time.Time   `json:"ts"`
	Data      interface{} `json:"data"`
}

// Store manages persistent state: an append-only JSONL log and a JSON snapshot
type Store struct {
	mu       sync.Mutex
	hiveDir  string
	logFile  *os.File
	eventSeq int
	snapshot map[string]interface{}
}

func NewStore(hiveDir string) (*Store, error) {
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		return nil, fmt.Errorf("create hive dir: %w", err)
	}

	logPath := filepath.Join(hiveDir, "log.jsonl")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}

	s := &Store{
		hiveDir:  hiveDir,
		logFile:  f,
		snapshot: make(map[string]interface{}),
	}

	// Replay existing log to get sequence number
	if err := s.replayLog(); err != nil {
		f.Close()
		return nil, err
	}

	// Load snapshot if exists
	s.loadSnapshot()

	return s, nil
}

func (s *Store) replayLog() error {
	logPath := filepath.Join(s.hiveDir, "log.jsonl")
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	count := 0
	for scanner.Scan() {
		count++
	}
	s.eventSeq = count
	return scanner.Err()
}

func (s *Store) loadSnapshot() {
	data, err := os.ReadFile(filepath.Join(s.hiveDir, "state.json"))
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.snapshot)
}

// Append writes an event to the JSONL log
func (s *Store) Append(eventType string, data interface{}) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.eventSeq++
	ev := Event{
		ID:        s.eventSeq,
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return ev, fmt.Errorf("marshal event: %w", err)
	}
	line = append(line, '\n')

	if _, err := s.logFile.Write(line); err != nil {
		return ev, fmt.Errorf("write event: %w", err)
	}

	return ev, nil
}

// SetSnapshot updates the in-memory snapshot and persists it
func (s *Store) SetSnapshot(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot[key] = value
	return s.persistSnapshot()
}

// GetSnapshot retrieves a value from the snapshot
func (s *Store) GetSnapshot(key string) (interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.snapshot[key]
	return v, ok
}

func (s *Store) persistSnapshot() error {
	data, err := json.MarshalIndent(s.snapshot, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.hiveDir, "state.json")
	return os.WriteFile(path, data, 0644)
}

// ReadLog reads back all events from the JSONL log
func (s *Store) ReadLog() ([]Event, error) {
	logPath := filepath.Join(s.hiveDir, "log.jsonl")
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

// Close flushes and closes the log file
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistSnapshot()
	return s.logFile.Close()
}

// EventCount returns the total number of events logged
func (s *Store) EventCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eventSeq
}
