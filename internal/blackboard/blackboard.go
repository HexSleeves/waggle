package blackboard

import (
	"sync"
	"time"

	"github.com/exedev/queen-bee/internal/bus"
)

// Entry is a single item posted to the blackboard
type Entry struct {
	Key       string      `json:"key"`
	Value     interface{} `json:"value"`
	PostedBy  string      `json:"posted_by"`
	TaskID    string      `json:"task_id,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
	Tags      []string    `json:"tags,omitempty"`
}

// Blackboard is a shared memory space where workers post partial results
type Blackboard struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	history []*Entry
	bus     *bus.MessageBus
}

func New(b *bus.MessageBus) *Blackboard {
	return &Blackboard{
		entries: make(map[string]*Entry),
		bus:     b,
	}
}

// Post adds or updates an entry on the blackboard
func (bb *Blackboard) Post(entry *Entry) {
	bb.mu.Lock()
	defer bb.mu.Unlock()

	entry.Timestamp = time.Now()
	bb.entries[entry.Key] = entry
	bb.history = append(bb.history, entry)

	if bb.bus != nil {
		bb.bus.Publish(bus.Message{
			Type:     bus.MsgBlackboardUpdate,
			WorkerID: entry.PostedBy,
			TaskID:   entry.TaskID,
			Payload:  entry,
			Time:     entry.Timestamp,
		})
	}
}

// Read retrieves an entry by key
func (bb *Blackboard) Read(key string) (*Entry, bool) {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	e, ok := bb.entries[key]
	return e, ok
}

// ReadByTag returns all entries matching a tag
func (bb *Blackboard) ReadByTag(tag string) []*Entry {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	var result []*Entry
	for _, e := range bb.entries {
		for _, t := range e.Tags {
			if t == tag {
				result = append(result, e)
				break
			}
		}
	}
	return result
}

// ReadByWorker returns all entries posted by a specific worker
func (bb *Blackboard) ReadByWorker(workerID string) []*Entry {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	var result []*Entry
	for _, e := range bb.entries {
		if e.PostedBy == workerID {
			result = append(result, e)
		}
	}
	return result
}

// ReadByTask returns all entries related to a specific task
func (bb *Blackboard) ReadByTask(taskID string) []*Entry {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	var result []*Entry
	for _, e := range bb.entries {
		if e.TaskID == taskID {
			result = append(result, e)
		}
	}
	return result
}

// Keys returns all current keys
func (bb *Blackboard) Keys() []string {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	keys := make([]string, 0, len(bb.entries))
	for k := range bb.entries {
		keys = append(keys, k)
	}
	return keys
}

// All returns all current entries
func (bb *Blackboard) All() map[string]*Entry {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	cp := make(map[string]*Entry, len(bb.entries))
	for k, v := range bb.entries {
		cp[k] = v
	}
	return cp
}

// History returns the full history of posts
func (bb *Blackboard) History() []*Entry {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	cp := make([]*Entry, len(bb.history))
	copy(cp, bb.history)
	return cp
}

// Clear removes all entries
func (bb *Blackboard) Clear() {
	bb.mu.Lock()
	defer bb.mu.Unlock()
	bb.entries = make(map[string]*Entry)
}

// Summarize returns a text summary of all entries (for context compaction)
func (bb *Blackboard) Summarize() string {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	if len(bb.entries) == 0 {
		return "Blackboard is empty."
	}
	summary := "Blackboard contents:\n"
	for k, e := range bb.entries {
		summary += "- [" + k + "] by " + e.PostedBy
		if e.TaskID != "" {
			summary += " (task: " + e.TaskID + ")"
		}
		summary += "\n"
	}
	return summary
}
