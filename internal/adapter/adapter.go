package adapter

import (
	"context"

	"github.com/exedev/queen-bee/internal/task"
	"github.com/exedev/queen-bee/internal/worker"
)

// Adapter wraps a CLI tool to provide a standard worker interface
type Adapter interface {
	// Name returns the adapter identifier
	Name() string
	// Available checks if the underlying CLI tool is installed
	Available() bool
	// CreateWorker creates a new Bee backed by this adapter
	CreateWorker(id string) worker.Bee
}

// Registry holds all available adapters
type Registry struct {
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{
		adapters: make(map[string]Adapter),
	}
}

func (r *Registry) Register(a Adapter) {
	r.adapters[a.Name()] = a
}

func (r *Registry) Get(name string) (Adapter, bool) {
	a, ok := r.adapters[name]
	return a, ok
}

func (r *Registry) Available() []string {
	var names []string
	for _, a := range r.adapters {
		if a.Available() {
			names = append(names, a.Name())
		}
	}
	return names
}

// WorkerFactory returns a worker.Factory that creates workers from the registry
func (r *Registry) WorkerFactory() worker.Factory {
	return func(id string, adapterName string) (worker.Bee, error) {
		a, ok := r.adapters[adapterName]
		if !ok {
			return nil, context.DeadlineExceeded // placeholder
		}
		return a.CreateWorker(id), nil
	}
}

// TaskRouter determines which adapter to use for a given task type
type TaskRouter struct {
	registry *Registry
	routes   map[task.Type]string
}

func NewTaskRouter(reg *Registry) *TaskRouter {
	return &TaskRouter{
		registry: reg,
		routes: map[task.Type]string{
			task.TypeCode:     "claude-code",
			task.TypeResearch: "claude-code",
			task.TypeTest:     "claude-code",
			task.TypeReview:   "claude-code",
			task.TypeGeneric:  "claude-code",
		},
	}
}

func (tr *TaskRouter) SetRoute(taskType task.Type, adapterName string) {
	tr.routes[taskType] = adapterName
}

func (tr *TaskRouter) Route(t *task.Task) string {
	if name, ok := tr.routes[t.Type]; ok {
		if _, avail := tr.registry.Get(name); avail {
			return name
		}
	}
	// Fallback to first available
	avail := tr.registry.Available()
	if len(avail) > 0 {
		return avail[0]
	}
	return ""
}
