package blackboard

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/bus"
)

func TestNew(t *testing.T) {
	b := bus.New(100)
	bb := New(b)

	if bb == nil {
		t.Fatal("New returned nil")
	}
	if bb.entries == nil {
		t.Error("entries map not initialized")
	}
	if bb.bus != b {
		t.Error("bus not set correctly")
	}
}

func TestBlackboardPostAndRead(t *testing.T) {
	bb := New(nil)

	entry := &Entry{
		Key:      "config",
		Value:    map[string]string{"key": "value"},
		PostedBy: "worker-1",
		TaskID:   "task-1",
		Tags:     []string{"config", "important"},
	}

	bb.Post(entry)

	// Read back
	retrieved, ok := bb.Read("config")
	if !ok {
		t.Fatal("Expected to find entry")
	}
	if retrieved.Key != "config" {
		t.Errorf("Expected key 'config', got %s", retrieved.Key)
	}
	if retrieved.PostedBy != "worker-1" {
		t.Errorf("Expected PostedBy 'worker-1', got %s", retrieved.PostedBy)
	}
	if retrieved.TaskID != "task-1" {
		t.Errorf("Expected TaskID 'task-1', got %s", retrieved.TaskID)
	}
	if len(retrieved.Tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(retrieved.Tags))
	}
	if retrieved.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}

	// Read non-existent
	_, ok = bb.Read("non-existent")
	if ok {
		t.Error("Expected not to find non-existent entry")
	}
}

func TestBlackboardPostUpdatesTimestamp(t *testing.T) {
	bb := New(nil)

	beforePost := time.Now()
	time.Sleep(5 * time.Millisecond)

	entry := &Entry{
		Key:      "test",
		Value:    "value",
		PostedBy: "worker-1",
	}

	bb.Post(entry)

	time.Sleep(5 * time.Millisecond)
	afterPost := time.Now()

	retrieved, _ := bb.Read("test")
	if retrieved.Timestamp.Before(beforePost) || retrieved.Timestamp.After(afterPost) {
		t.Error("Timestamp not set correctly")
	}
}

func TestBlackboardPostWithBus(t *testing.T) {
	b := bus.New(100)
	var receivedMsg *bus.Message
	var mu sync.Mutex

	b.Subscribe(bus.MsgBlackboardUpdate, func(msg bus.Message) {
		mu.Lock()
		defer mu.Unlock()
		receivedMsg = &msg
	})

	bb := New(b)
	entry := &Entry{
		Key:      "result",
		Value:    "success",
		PostedBy: "worker-1",
		TaskID:   "task-1",
	}

	bb.Post(entry)

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	if receivedMsg == nil {
		t.Error("Expected message to be published")
	} else {
		if receivedMsg.Type != bus.MsgBlackboardUpdate {
			t.Errorf("Expected type %s, got %s", bus.MsgBlackboardUpdate, receivedMsg.Type)
		}
		if receivedMsg.WorkerID != "worker-1" {
			t.Errorf("Expected WorkerID 'worker-1', got %s", receivedMsg.WorkerID)
		}
		if receivedMsg.TaskID != "task-1" {
			t.Errorf("Expected TaskID 'task-1', got %s", receivedMsg.TaskID)
		}
		payload, ok := receivedMsg.Payload.(*Entry)
		if !ok {
			t.Error("Payload is not *Entry")
		} else if payload.Key != "result" {
			t.Errorf("Expected payload key 'result', got %s", payload.Key)
		}
	}
	mu.Unlock()
}

func TestBlackboardPostOverwrites(t *testing.T) {
	bb := New(nil)

	entry1 := &Entry{
		Key:      "config",
		Value:    "value1",
		PostedBy: "worker-1",
	}
	bb.Post(entry1)

	entry2 := &Entry{
		Key:      "config",
		Value:    "value2",
		PostedBy: "worker-2",
	}
	bb.Post(entry2)

	retrieved, _ := bb.Read("config")
	if retrieved.Value != "value2" {
		t.Errorf("Expected value 'value2', got %v", retrieved.Value)
	}
	if retrieved.PostedBy != "worker-2" {
		t.Errorf("Expected PostedBy 'worker-2', got %s", retrieved.PostedBy)
	}

	// History should have both entries
	history := bb.History()
	if len(history) != 2 {
		t.Errorf("Expected 2 history entries, got %d", len(history))
	}
}

func TestBlackboardReadByTag(t *testing.T) {
	bb := New(nil)

	entries := []*Entry{
		{Key: "config1", Value: "v1", PostedBy: "w1", Tags: []string{"config"}},
		{Key: "config2", Value: "v2", PostedBy: "w2", Tags: []string{"config", "important"}},
		{Key: "result1", Value: "r1", PostedBy: "w3", Tags: []string{"result"}},
		{Key: "config3", Value: "v3", PostedBy: "w4", Tags: []string{"config"}},
	}

	for _, e := range entries {
		bb.Post(e)
	}

	// Find by "config" tag
	configEntries := bb.ReadByTag("config")
	if len(configEntries) != 3 {
		t.Errorf("Expected 3 config entries, got %d", len(configEntries))
	}

	// Find by "important" tag
	importantEntries := bb.ReadByTag("important")
	if len(importantEntries) != 1 {
		t.Errorf("Expected 1 important entry, got %d", len(importantEntries))
	}
	if importantEntries[0].Key != "config2" {
		t.Errorf("Expected key 'config2', got %s", importantEntries[0].Key)
	}

	// Find by non-existent tag
	none := bb.ReadByTag("non-existent")
	if len(none) != 0 {
		t.Errorf("Expected 0 entries, got %d", len(none))
	}
}

func TestBlackboardReadByWorker(t *testing.T) {
	bb := New(nil)

	entries := []*Entry{
		{Key: "e1", Value: "v1", PostedBy: "worker-1", TaskID: "task-1"},
		{Key: "e2", Value: "v2", PostedBy: "worker-2", TaskID: "task-1"},
		{Key: "e3", Value: "v3", PostedBy: "worker-1", TaskID: "task-2"},
		{Key: "e4", Value: "v4", PostedBy: "worker-1", TaskID: "task-3"},
	}

	for _, e := range entries {
		bb.Post(e)
	}

	worker1Entries := bb.ReadByWorker("worker-1")
	if len(worker1Entries) != 3 {
		t.Errorf("Expected 3 entries for worker-1, got %d", len(worker1Entries))
	}

	worker2Entries := bb.ReadByWorker("worker-2")
	if len(worker2Entries) != 1 {
		t.Errorf("Expected 1 entry for worker-2, got %d", len(worker2Entries))
	}

	none := bb.ReadByWorker("worker-3")
	if len(none) != 0 {
		t.Errorf("Expected 0 entries for worker-3, got %d", len(none))
	}
}

func TestBlackboardReadByTask(t *testing.T) {
	bb := New(nil)

	entries := []*Entry{
		{Key: "e1", Value: "v1", PostedBy: "worker-1", TaskID: "task-1"},
		{Key: "e2", Value: "v2", PostedBy: "worker-2", TaskID: "task-2"},
		{Key: "e3", Value: "v3", PostedBy: "worker-3", TaskID: "task-1"},
	}

	for _, e := range entries {
		bb.Post(e)
	}

	task1Entries := bb.ReadByTask("task-1")
	if len(task1Entries) != 2 {
		t.Errorf("Expected 2 entries for task-1, got %d", len(task1Entries))
	}

	task2Entries := bb.ReadByTask("task-2")
	if len(task2Entries) != 1 {
		t.Errorf("Expected 1 entry for task-2, got %d", len(task2Entries))
	}

	none := bb.ReadByTask("task-3")
	if len(none) != 0 {
		t.Errorf("Expected 0 entries for task-3, got %d", len(none))
	}
}

func TestBlackboardKeys(t *testing.T) {
	bb := New(nil)

	if len(bb.Keys()) != 0 {
		t.Error("Expected empty keys initially")
	}

	bb.Post(&Entry{Key: "key1", Value: "v1", PostedBy: "w1"})
	bb.Post(&Entry{Key: "key2", Value: "v2", PostedBy: "w2"})
	bb.Post(&Entry{Key: "key3", Value: "v3", PostedBy: "w3"})

	keys := bb.Keys()
	if len(keys) != 3 {
		t.Errorf("Expected 3 keys, got %d", len(keys))
	}

	keyMap := make(map[string]bool)
	for _, k := range keys {
		keyMap[k] = true
	}

	if !keyMap["key1"] || !keyMap["key2"] || !keyMap["key3"] {
		t.Error("Missing expected keys")
	}
}

func TestBlackboardAll(t *testing.T) {
	bb := New(nil)

	bb.Post(&Entry{Key: "key1", Value: "v1", PostedBy: "w1"})
	bb.Post(&Entry{Key: "key2", Value: "v2", PostedBy: "w2"})

	all := bb.All()
	if len(all) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(all))
	}

	// Verify it's a copy - modifying returned map shouldn't affect original
	for k := range all {
		delete(all, k)
	}

	// Original should still have entries
	if len(bb.entries) != 2 {
		t.Error("Original entries modified")
	}
}

func TestBlackboardHistory(t *testing.T) {
	bb := New(nil)

	if len(bb.History()) != 0 {
		t.Error("Expected empty history initially")
	}

	// Post multiple entries with same key (should create history)
	bb.Post(&Entry{Key: "config", Value: "v1", PostedBy: "w1"})
	bb.Post(&Entry{Key: "config", Value: "v2", PostedBy: "w2"})
	bb.Post(&Entry{Key: "result", Value: "r1", PostedBy: "w3"})

	history := bb.History()
	if len(history) != 3 {
		t.Errorf("Expected 3 history entries, got %d", len(history))
	}

	// Verify it's a copy
	history[0].Key = "modified"
	if bb.history[0].Key == "modified" {
		t.Error("History should return a copy")
	}
}

func TestBlackboardClear(t *testing.T) {
	bb := New(nil)

	bb.Post(&Entry{Key: "key1", Value: "v1", PostedBy: "w1"})
	bb.Post(&Entry{Key: "key2", Value: "v2", PostedBy: "w2"})

	if len(bb.entries) != 2 {
		t.Fatal("Setup failed")
	}

	bb.Clear()

	if len(bb.entries) != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", len(bb.entries))
	}

	// History should remain
	if len(bb.history) != 2 {
		t.Errorf("History should remain, expected 2 entries, got %d", len(bb.history))
	}
}

func TestBlackboardSummarize(t *testing.T) {
	bb := New(nil)

	// Empty blackboard
	summary := bb.Summarize()
	if summary != "Blackboard is empty." {
		t.Errorf("Expected empty summary, got: %s", summary)
	}

	// Add entries
	bb.Post(&Entry{Key: "config", Value: "v1", PostedBy: "worker-1", TaskID: "task-1"})
	bb.Post(&Entry{Key: "result", Value: "v2", PostedBy: "worker-2"})

	summary = bb.Summarize()
	if summary == "" {
		t.Error("Expected non-empty summary")
	}
	if summary == "Blackboard is empty." {
		t.Error("Summary shouldn't indicate empty")
	}
	if !strings.HasPrefix(summary, "Blackboard contents") {
		t.Errorf("Expected 'Blackboard contents' prefix, got: %s", summary)
	}

	// Check that keys are included
	if !strings.Contains(summary, "[config]") && !strings.Contains(summary, "[result]") {
		t.Errorf("Summary should contain entry keys, got: %s", summary)
	}
}

func TestBlackboardConcurrency(t *testing.T) {
	bb := New(nil)
	var wg sync.WaitGroup

	// Concurrent posts
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bb.Post(&Entry{
				Key:      string(rune('a' + id%26)),
				Value:    id,
				PostedBy: "worker",
			})
		}(i)
	}

	wg.Wait()

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bb.Read(string(rune('a' + id%26)))
			bb.Keys()
			bb.All()
		}(i)
	}

	wg.Wait()

	// Concurrent ReadBy operations
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			bb.ReadByTag("tag")
		}()
		go func() {
			defer wg.Done()
			bb.ReadByWorker("worker")
		}()
		go func() {
			defer wg.Done()
			bb.ReadByTask("task")
		}()
	}

	wg.Wait()

	// Concurrent operations that modify
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bb.Clear()
		}()
	}

	wg.Wait()
}

func TestEntryStruct(t *testing.T) {
	entry := &Entry{
		Key:       "test-key",
		Value:     map[string]int{"count": 42},
		PostedBy:  "worker-1",
		TaskID:    "task-1",
		Timestamp: time.Now(),
		Tags:      []string{"important", "config"},
	}

	if entry.Key != "test-key" {
		t.Error("Key mismatch")
	}
	if entry.PostedBy != "worker-1" {
		t.Error("PostedBy mismatch")
	}
	if len(entry.Tags) != 2 {
		t.Error("Tags length mismatch")
	}
}

func TestBlackboardDeepCopyRead(t *testing.T) {
	bb := New(nil)

	originalValue := map[string]int{"count": 42}
	entry := &Entry{
		Key:      "test",
		Value:    originalValue,
		PostedBy: "worker-1",
	}
	bb.Post(entry)

	retrieved, ok := bb.Read("test")
	if !ok {
		t.Fatal("Expected to find entry")
	}

	if retrieved != bb.entries["test"] {
		t.Error("Read returns pointer to internal entry")
	}

	retrieved.Value.(map[string]int)["count"] = 100
	if originalValue["count"] != 100 {
		t.Error("Value should be shared (not deep copied)")
	}
}

func TestBlackboardDeepCopyAll(t *testing.T) {
	bb := New(nil)

	originalValue := map[string]int{"count": 42}
	entry := &Entry{
		Key:      "test",
		Value:    originalValue,
		PostedBy: "worker-1",
	}
	bb.Post(entry)

	all := bb.All()
	allEntry := all["test"]

	if allEntry != bb.entries["test"] {
		t.Error("All() returns different Entry pointers")
	}

	allEntry.Value.(map[string]int)["count"] = 100

	retrieved, _ := bb.Read("test")
	if retrieved.Value.(map[string]int)["count"] != 100 {
		t.Error("All() should return entries that share the same underlying data")
	}
}

func TestBlackboardDeepCopyReadByTag(t *testing.T) {
	bb := New(nil)

	entry := &Entry{
		Key:      "test",
		Value:    "original",
		PostedBy: "worker-1",
		Tags:     []string{"tag1"},
	}
	bb.Post(entry)

	entries := bb.ReadByTag("tag1")
	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}

	if entries[0] != bb.entries["test"] {
		t.Error("ReadByTag returns different Entry pointers")
	}
}

func TestBlackboardDeepCopyReadByWorker(t *testing.T) {
	bb := New(nil)

	entry := &Entry{
		Key:      "test",
		Value:    "original",
		PostedBy: "worker-1",
	}
	bb.Post(entry)

	entries := bb.ReadByWorker("worker-1")
	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}

	if entries[0] != bb.entries["test"] {
		t.Error("ReadByWorker returns different Entry pointers")
	}
}

func TestBlackboardDeepCopyReadByTask(t *testing.T) {
	bb := New(nil)

	entry := &Entry{
		Key:      "test",
		Value:    "original",
		PostedBy: "worker-1",
		TaskID:   "task-1",
	}
	bb.Post(entry)

	entries := bb.ReadByTask("task-1")
	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}

	if entries[0] != bb.entries["test"] {
		t.Error("ReadByTask returns different Entry pointers")
	}
}

func TestBlackboardConcurrentPostAndRead(t *testing.T) {
	bb := New(nil)
	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", id)
			bb.Post(&Entry{
				Key:      key,
				Value:    id,
				PostedBy: "worker",
			})
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", id)
			bb.Read(key)
			bb.Keys()
			bb.All()
			bb.ReadByWorker("worker")
			bb.ReadByTag("tag")
			bb.ReadByTask("task")
		}(i)
	}

	wg.Wait()

	if len(bb.Keys()) != numGoroutines {
		t.Errorf("Expected %d keys, got %d", numGoroutines, len(bb.Keys()))
	}
}

func TestBlackboardConcurrentPostAndHistory(t *testing.T) {
	bb := New(nil)
	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bb.Post(&Entry{
				Key:      fmt.Sprintf("key-%d", id%10),
				Value:    id,
				PostedBy: "worker",
			})
		}(i)
	}

	wg.Wait()

	history := bb.History()
	if len(history) != numGoroutines {
		t.Errorf("Expected %d history entries, got %d", numGoroutines, len(history))
	}
}

func TestBlackboardConcurrentMixedOperations(t *testing.T) {
	bb := New(nil)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				bb.Post(&Entry{
					Key:      fmt.Sprintf("key-%d", i%20),
					Value:    i,
					PostedBy: "worker",
					Tags:     []string{fmt.Sprintf("tag-%d", i%5)},
				})
			}
		}
	}()

	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				bb.Read(fmt.Sprintf("key-%d", i%20))
				bb.Keys()
				bb.All()
				bb.History()
				bb.ReadByWorker("worker")
				bb.ReadByTag(fmt.Sprintf("tag-%d", i%5))
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestBlackboardResultPersistence(t *testing.T) {
	bb := New(nil)

	workerResults := []*Entry{
		{Key: "task-1-result", Value: `{"status": "success", "output": "Task 1 completed"}`, PostedBy: "worker-1", TaskID: "task-1", Tags: []string{"result"}},
		{Key: "task-2-result", Value: `{"status": "success", "output": "Task 2 completed"}`, PostedBy: "worker-2", TaskID: "task-2", Tags: []string{"result"}},
		{Key: "task-3-result", Value: `{"status": "failed", "error": "Task 3 failed"}`, PostedBy: "worker-3", TaskID: "task-3", Tags: []string{"result", "failed"}},
	}

	for _, e := range workerResults {
		bb.Post(e)
	}

	allEntries := bb.All()
	if len(allEntries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(allEntries))
	}

	for _, e := range workerResults {
		key := e.Key
		retrieved, ok := bb.Read(key)
		if !ok {
			t.Errorf("Expected to find entry %s", key)
		}
		if retrieved.Value != e.Value {
			t.Errorf("Expected value %s, got %s", e.Value, retrieved.Value)
		}
		if retrieved.PostedBy != e.PostedBy {
			t.Errorf("Expected PostedBy %s, got %s", e.PostedBy, retrieved.PostedBy)
		}
		if retrieved.TaskID != e.TaskID {
			t.Errorf("Expected TaskID %s, got %s", e.TaskID, retrieved.TaskID)
		}
	}
}

func TestBlackboardResultPersistenceAcrossSessionResume(t *testing.T) {
	bb1 := New(nil)

	originalResults := []*Entry{
		{Key: "task-1", Value: "result-1", PostedBy: "worker-1", TaskID: "task-1"},
		{Key: "task-2", Value: "result-2", PostedBy: "worker-2", TaskID: "task-2"},
		{Key: "task-3", Value: "result-3", PostedBy: "worker-1", TaskID: "task-3"},
	}

	for _, e := range originalResults {
		bb1.Post(e)
	}

	entriesForPersistence := bb1.All()
	historyForPersistence := bb1.History()

	bb2 := New(nil)

	for _, e := range entriesForPersistence {
		bb2.Post(&Entry{
			Key:      e.Key,
			Value:    e.Value,
			PostedBy: e.PostedBy,
			TaskID:   e.TaskID,
			Tags:     e.Tags,
		})
	}

	if len(bb2.Keys()) != len(originalResults) {
		t.Errorf("Expected %d keys after resume, got %d", len(originalResults), len(bb2.Keys()))
	}

	for _, orig := range originalResults {
		retrieved, ok := bb2.Read(orig.Key)
		if !ok {
			t.Errorf("Expected to find entry %s after resume", orig.Key)
		}
		if retrieved.Value != orig.Value {
			t.Errorf("Expected value %s, got %s", orig.Value, retrieved.Value)
		}
	}

	resumeHistory := bb2.History()
	if len(resumeHistory) != len(originalResults) {
		t.Errorf("Expected %d history entries, got %d", len(originalResults), len(resumeHistory))
	}

	_ = historyForPersistence
}
