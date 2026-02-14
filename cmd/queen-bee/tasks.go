package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/exedev/queen-bee/internal/config"
	"github.com/exedev/queen-bee/internal/task"
)

func loadTasksFile(path string, cfg *config.Config) ([]*task.Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	type rawTask struct {
		ID          string   `json:"id"`
		Type        string   `json:"type"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Priority    int      `json:"priority"`
		DependsOn   []string `json:"depends_on"`
		MaxRetries  int      `json:"max_retries"`
	}

	var rawTasks []rawTask
	if err := json.Unmarshal(data, &rawTasks); err != nil {
		return nil, fmt.Errorf("parse tasks JSON: %w", err)
	}

	tasks := make([]*task.Task, 0, len(rawTasks))
	for _, rt := range rawTasks {
		t := &task.Task{
			ID:          rt.ID,
			Type:        task.Type(rt.Type),
			Status:      task.StatusPending,
			Priority:    task.Priority(rt.Priority),
			Title:       rt.Title,
			Description: rt.Description,
			DependsOn:   rt.DependsOn,
			MaxRetries:  rt.MaxRetries,
			CreatedAt:   time.Now(),
			Timeout:     cfg.Workers.DefaultTimeout,
		}
		if t.MaxRetries == 0 {
			t.MaxRetries = cfg.Workers.MaxRetries
		}
		if t.ID == "" {
			t.ID = fmt.Sprintf("task-%d", time.Now().UnixNano())
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}
