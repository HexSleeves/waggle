package queen

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/exedev/waggle/internal/adapter"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// MockRegistry implements adapter.Registry functionality with configurable adapter availability
type MockRegistry struct {
	mu        sync.RWMutex
	adapters  map[string]adapter.Adapter
	available map[string]bool
}

// NewMockRegistry creates a new MockRegistry with no adapters registered
func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		adapters:  make(map[string]adapter.Adapter),
		available: make(map[string]bool),
	}
}

// Register adds an adapter to the registry
func (r *MockRegistry) Register(a adapter.Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Name()] = a
	// By default, adapters are available if they report as available
	r.available[a.Name()] = a.Available()
}

// Get retrieves an adapter by name
func (r *MockRegistry) Get(name string) (adapter.Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// Available returns a list of available adapter names
func (r *MockRegistry) Available() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name, a := range r.adapters {
		if r.available[name] && a.Available() {
			names = append(names, name)
		}
	}
	return names
}

// SetAvailable configures whether an adapter is available
func (r *MockRegistry) SetAvailable(name string, available bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.available[name] = available
}

// IsAvailable checks if an adapter is configured as available
func (r *MockRegistry) IsAvailable(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.available[name]
}

// WorkerFactory returns a worker.Factory that creates workers from the registry
func (r *MockRegistry) WorkerFactory() worker.Factory {
	return func(id string, adapterName string) (worker.Bee, error) {
		r.mu.RLock()
		defer r.mu.RUnlock()

		a, ok := r.adapters[adapterName]
		if !ok {
			return nil, fmt.Errorf("adapter %q not registered", adapterName)
		}
		if !r.available[adapterName] {
			return nil, fmt.Errorf("adapter %q not available", adapterName)
		}
		return a.CreateWorker(id), nil
	}
}

// AdapterNames returns all registered adapter names (for inspection)
func (r *MockRegistry) AdapterNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name := range r.adapters {
		names = append(names, name)
	}
	return names
}

// Clear removes all adapters from the registry
func (r *MockRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters = make(map[string]adapter.Adapter)
	r.available = make(map[string]bool)
}

// MockAdapter is a configurable mock adapter for testing
type MockAdapter struct {
	name          string
	available     bool
	workerFactory func(id string) worker.Bee
}

// NewMockAdapter creates a new MockAdapter
func NewMockAdapter(name string, available bool) *MockAdapter {
	return &MockAdapter{
		name:      name,
		available: available,
		workerFactory: func(id string) worker.Bee {
			return NewEnhancedMockBee(id, name)
		},
	}
}

// Name returns the adapter name
func (m *MockAdapter) Name() string {
	return m.name
}

// Available returns whether the adapter is available
func (m *MockAdapter) Available() bool {
	return m.available
}

// CreateWorker creates a new worker.Bee
func (m *MockAdapter) CreateWorker(id string) worker.Bee {
	if m.workerFactory != nil {
		return m.workerFactory(id)
	}
	return NewEnhancedMockBee(id, m.name)
}

// SetWorkerFactory configures a custom worker factory
func (m *MockAdapter) SetWorkerFactory(factory func(id string) worker.Bee) {
	m.workerFactory = factory
}

// SetAvailable configures adapter availability
func (m *MockAdapter) SetAvailable(available bool) {
	m.available = available
}

// EnhancedMockBee is a configurable mock implementation of worker.Bee
type EnhancedMockBee struct {
	mu sync.RWMutex

	// Identity
	id  string
	typ string

	// Configurable behavior
	status           worker.Status
	result           *task.Result
	output           string
	spawnError       error
	killError        error
	delay            time.Duration
	autoComplete     bool
	statusTransition []worker.Status

	// Call tracking for inspection
	spawnCalled    bool
	killCalled     bool
	spawnTask      *task.Task
	spawnContext   context.Context
	spawnCallCount int

	// Status control channel
	statusCh chan worker.Status
}

// NewEnhancedMockBee creates a new EnhancedMockBee with default settings
func NewEnhancedMockBee(id, typ string) *EnhancedMockBee {
	return &EnhancedMockBee{
		id:           id,
		typ:          typ,
		status:       worker.StatusIdle,
		result:       &task.Result{Success: true, Output: "mock output"},
		output:       "mock output",
		autoComplete: true,
		statusCh:     make(chan worker.Status, 10),
	}
}

// ID returns the worker identifier
func (m *EnhancedMockBee) ID() string {
	return m.id
}

// Type returns the worker type
func (m *EnhancedMockBee) Type() string {
	return m.typ
}

// Spawn starts the worker with a task
func (m *EnhancedMockBee) Spawn(ctx context.Context, t *task.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.spawnCalled = true
	m.spawnCallCount++
	m.spawnTask = t
	m.spawnContext = ctx

	if m.spawnError != nil {
		m.status = worker.StatusFailed
		return m.spawnError
	}

	m.status = worker.StatusRunning

	// Simulate async work if autoComplete is enabled
	if m.autoComplete {
		go m.runSimulation(ctx)
	}

	return nil
}

// runSimulation simulates task execution with configurable delay and transitions
func (m *EnhancedMockBee) runSimulation(ctx context.Context) {
	// Apply initial delay if configured
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			m.mu.Lock()
			m.status = worker.StatusFailed
			m.mu.Unlock()
			return
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Process status transitions if configured
	if len(m.statusTransition) > 0 {
		for _, status := range m.statusTransition {
			m.status = status
			select {
			case m.statusCh <- status:
			default:
			}
			// Small delay between transitions for realism
			time.Sleep(10 * time.Millisecond)
		}
	} else {
		// Default transition: running -> complete/failed
		if m.result != nil && m.result.Success {
			m.status = worker.StatusComplete
		} else {
			m.status = worker.StatusFailed
		}
		select {
		case m.statusCh <- m.status:
		default:
		}
	}
}

// Monitor returns the current status
func (m *EnhancedMockBee) Monitor() worker.Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// Result returns the task result
func (m *EnhancedMockBee) Result() *task.Result {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.result
}

// Kill terminates the worker
func (m *EnhancedMockBee) Kill() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.killCalled = true
	m.status = worker.StatusFailed

	return m.killError
}

// Output returns accumulated output
func (m *EnhancedMockBee) Output() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.output
}

// Configuration methods

// SetStatus sets the current status (for manual control)
func (m *EnhancedMockBee) SetStatus(status worker.Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = status
	select {
	case m.statusCh <- status:
	default:
	}
}

// SetResult configures the result to return
func (m *EnhancedMockBee) SetResult(result *task.Result) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result = result
}

// SetOutput configures the output string
func (m *EnhancedMockBee) SetOutput(output string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.output = output
}

// SetSpawnError configures an error to return from Spawn
func (m *EnhancedMockBee) SetSpawnError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spawnError = err
}

// SetKillError configures an error to return from Kill
func (m *EnhancedMockBee) SetKillError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.killError = err
}

// SetDelay configures the delay before status changes
func (m *EnhancedMockBee) SetDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = delay
}

// SetAutoComplete configures whether to auto-complete on spawn
func (m *EnhancedMockBee) SetAutoComplete(auto bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoComplete = auto
}

// SetStatusTransition configures a sequence of status transitions
func (m *EnhancedMockBee) SetStatusTransition(transitions []worker.Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusTransition = transitions
}

// Inspection methods

// WasSpawnCalled returns true if Spawn was called
func (m *EnhancedMockBee) WasSpawnCalled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.spawnCalled
}

// WasKillCalled returns true if Kill was called
func (m *EnhancedMockBee) WasKillCalled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.killCalled
}

// GetSpawnTask returns the task passed to Spawn
func (m *EnhancedMockBee) GetSpawnTask() *task.Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.spawnTask
}

// GetSpawnContext returns the context passed to Spawn
func (m *EnhancedMockBee) GetSpawnContext() context.Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.spawnContext
}

// GetSpawnCallCount returns the number of times Spawn was called
func (m *EnhancedMockBee) GetSpawnCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.spawnCallCount
}

// WaitForStatus blocks until the specified status is reached or timeout
func (m *EnhancedMockBee) WaitForStatus(status worker.Status, timeout time.Duration) bool {
	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return false
		case <-ticker.C:
			if m.Monitor() == status {
				return true
			}
		}
	}
}

// WaitForStatusCh blocks until the specified status is sent on statusCh or timeout
func (m *EnhancedMockBee) WaitForStatusCh(status worker.Status, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return false
		case s := <-m.statusCh:
			if s == status {
				return true
			}
		}
	}
}

// Reset resets the mock to its initial state
func (m *EnhancedMockBee) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.status = worker.StatusIdle
	m.result = &task.Result{Success: true, Output: "mock output"}
	m.output = "mock output"
	m.spawnError = nil
	m.killError = nil
	m.delay = 0
	m.autoComplete = true
	m.statusTransition = nil

	m.spawnCalled = false
	m.killCalled = false
	m.spawnTask = nil
	m.spawnContext = nil
	m.spawnCallCount = 0

	// Drain status channel
	select {
	case <-m.statusCh:
	default:
	}
}

// Preset configuration helpers

// Succeed configures the mock to succeed with the given output
func (m *EnhancedMockBee) Succeed(output string) {
	m.SetResult(&task.Result{Success: true, Output: output})
	m.SetOutput(output)
	m.SetStatus(worker.StatusComplete)
}

// Fail configures the mock to fail with the given error message
func (m *EnhancedMockBee) Fail(errMsg string) {
	m.SetResult(&task.Result{Success: false, Errors: []string{errMsg}})
	m.SetOutput("")
	m.SetStatus(worker.StatusFailed)
}

// FailWithOutput configures the mock to fail with output and errors
func (m *EnhancedMockBee) FailWithOutput(output string, errors []string) {
	m.SetResult(&task.Result{Success: false, Output: output, Errors: errors})
	m.SetOutput(output)
	m.SetStatus(worker.StatusFailed)
}

// SimulateStuck configures the mock to stay in running state
func (m *EnhancedMockBee) SimulateStuck() {
	m.SetAutoComplete(false)
	m.SetStatus(worker.StatusRunning)
}

// SimulateTimeout configures the mock to simulate a timeout scenario
func (m *EnhancedMockBee) SimulateTimeout() {
	m.SetDelay(1 * time.Hour) // Long delay to simulate timeout
	m.SetStatus(worker.StatusRunning)
}

// MockBeeFactory creates EnhancedMockBee instances with preset behaviors
type MockBeeFactory struct {
	mu      sync.Mutex
	bees    map[string]*EnhancedMockBee
	counter int
}

// NewMockBeeFactory creates a new MockBeeFactory
func NewMockBeeFactory() *MockBeeFactory {
	return &MockBeeFactory{
		bees: make(map[string]*EnhancedMockBee),
	}
}

// CreateBee creates a new EnhancedMockBee with the given behavior preset
func (f *MockBeeFactory) CreateBee(id string, preset string) *EnhancedMockBee {
	f.mu.Lock()
	defer f.mu.Unlock()

	bee := NewEnhancedMockBee(id, "mock")

	switch preset {
	case "success":
		bee.Succeed("Task completed successfully")
	case "failure":
		bee.Fail("Task failed")
	case "error":
		bee.SetSpawnError(fmt.Errorf("spawn error"))
	case "slow":
		bee.SetDelay(100 * time.Millisecond)
		bee.Succeed("Task completed after delay")
	case "stuck":
		bee.SimulateStuck()
	case "timeout":
		bee.SimulateTimeout()
	}

	f.bees[id] = bee
	return bee
}

// GetBee retrieves a previously created bee
func (f *MockBeeFactory) GetBee(id string) (*EnhancedMockBee, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bee, ok := f.bees[id]
	return bee, ok
}

// AllBees returns all created bees
func (f *MockBeeFactory) AllBees() []*EnhancedMockBee {
	f.mu.Lock()
	defer f.mu.Unlock()
	var bees []*EnhancedMockBee
	for _, bee := range f.bees {
		bees = append(bees, bee)
	}
	return bees
}

// Reset clears all created bees
func (f *MockBeeFactory) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bees = make(map[string]*EnhancedMockBee)
	f.counter = 0
}

// CreateBeeWithID creates a bee with an auto-generated ID
func (f *MockBeeFactory) CreateBeeWithID(preset string) *EnhancedMockBee {
	f.mu.Lock()
	f.counter++
	id := fmt.Sprintf("mock-bee-%d", f.counter)
	f.mu.Unlock()
	return f.CreateBee(id, preset)
}

// MockCallTracker tracks calls to mocks for verification
type MockCallTracker struct {
	mu    sync.Mutex
	calls []MockCall
}

// MockCall represents a single mock method call
type MockCall struct {
	Method    string
	Args      map[string]interface{}
	Timestamp time.Time
}

// NewMockCallTracker creates a new call tracker
func NewMockCallTracker() *MockCallTracker {
	return &MockCallTracker{
		calls: make([]MockCall, 0),
	}
}

// Record records a method call
func (t *MockCallTracker) Record(method string, args map[string]interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, MockCall{
		Method:    method,
		Args:      args,
		Timestamp: time.Now(),
	})
}

// GetCalls returns all recorded calls to a specific method
func (t *MockCallTracker) GetCalls(method string) []MockCall {
	t.mu.Lock()
	defer t.mu.Unlock()

	var result []MockCall
	for _, call := range t.calls {
		if call.Method == method {
			result = append(result, call)
		}
	}
	return result
}

// WasMethodCalled returns true if a method was called at least once
func (t *MockCallTracker) WasMethodCalled(method string) bool {
	return len(t.GetCalls(method)) > 0
}

// CallCount returns the number of times a method was called
func (t *MockCallTracker) CallCount(method string) int {
	return len(t.GetCalls(method))
}

// Reset clears all recorded calls
func (t *MockCallTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = make([]MockCall, 0)
}

// AllCalls returns all recorded calls
func (t *MockCallTracker) AllCalls() []MockCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]MockCall, len(t.calls))
	copy(result, t.calls)
	return result
}
