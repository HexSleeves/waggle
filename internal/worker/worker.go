package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/exedev/queen-bee/internal/bus"
	"github.com/exedev/queen-bee/internal/task"
)

// Status represents the current state of a worker
type Status string

const (
	StatusIdle     Status = "idle"
	StatusRunning  Status = "running"
	StatusStuck    Status = "stuck"
	StatusComplete Status = "complete"
	StatusFailed   Status = "failed"
)

// Bee is the interface all worker types must implement
type Bee interface {
	// ID returns the unique worker identifier
	ID() string
	// Type returns the worker type (coder, researcher, tester)
	Type() string
	// Spawn starts the worker with a task
	Spawn(ctx context.Context, t *task.Task) error
	// Monitor returns the current status
	Monitor() Status
	// Result returns the task result (only valid after completion)
	Result() *task.Result
	// Kill terminates the worker
	Kill() error
	// Output returns accumulated stdout/stderr
	Output() string
}

// Factory creates a Bee for a given adapter name
type Factory func(id string, adapterName string) (Bee, error)

// Pool manages a set of concurrent workers
type Pool struct {
	mu          sync.Mutex
	workers     map[string]Bee
	maxParallel int
	factory     Factory
	msgBus      *bus.MessageBus
}

func NewPool(maxParallel int, factory Factory, b *bus.MessageBus) *Pool {
	return &Pool{
		workers:     make(map[string]Bee),
		maxParallel: maxParallel,
		factory:     factory,
		msgBus:      b,
	}
}

// Spawn creates and starts a new worker for a task
func (p *Pool) Spawn(ctx context.Context, t *task.Task, adapterName string) (Bee, error) {
	p.mu.Lock()
	// Count active workers
	active := 0
	for _, w := range p.workers {
		if w.Monitor() == StatusRunning {
			active++
		}
	}
	if active >= p.maxParallel {
		p.mu.Unlock()
		return nil, fmt.Errorf("max parallel workers (%d) reached", p.maxParallel)
	}
	p.mu.Unlock()

	workerID := fmt.Sprintf("worker-%s-%d", t.Type, time.Now().UnixNano())
	bee, err := p.factory(workerID, adapterName)
	if err != nil {
		return nil, fmt.Errorf("create worker: %w", err)
	}

	p.mu.Lock()
	p.workers[workerID] = bee
	p.mu.Unlock()

	if p.msgBus != nil {
		p.msgBus.Publish(bus.Message{
			Type:     bus.MsgWorkerSpawned,
			WorkerID: workerID,
			TaskID:   t.ID,
			Time:     time.Now(),
		})
	}

	if err := bee.Spawn(ctx, t); err != nil {
		return nil, fmt.Errorf("spawn worker: %w", err)
	}

	return bee, nil
}

// Get returns a worker by ID
func (p *Pool) Get(id string) (Bee, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w, ok := p.workers[id]
	return w, ok
}

// Active returns currently running workers
func (p *Pool) Active() []Bee {
	p.mu.Lock()
	defer p.mu.Unlock()
	var active []Bee
	for _, w := range p.workers {
		if w.Monitor() == StatusRunning {
			active = append(active, w)
		}
	}
	return active
}

// ActiveCount returns the number of running workers
func (p *Pool) ActiveCount() int {
	return len(p.Active())
}

// KillAll terminates all running workers
func (p *Pool) KillAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		if w.Monitor() == StatusRunning {
			err := w.Kill()
			if err != nil {
				fmt.Printf("error killing worker %s: %v\n", w.ID(), err)
			}
		}
	}
}

// Cleanup removes completed/failed workers from the pool
func (p *Pool) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, w := range p.workers {
		s := w.Monitor()
		if s == StatusComplete || s == StatusFailed {
			delete(p.workers, id)
		}
	}
}
