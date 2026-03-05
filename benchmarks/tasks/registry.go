package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Registry loads and filters benchmark tasks from a directory.
type Registry struct {
	tasks []Task
}

// LoadFromDir loads all .json task files from a directory.
func LoadFromDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read task dir %s: %w", dir, err)
	}

	var tasks []Task
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read task file %s: %w", entry.Name(), err)
		}

		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("parse task file %s: %w", entry.Name(), err)
		}

		if err := validateTask(t); err != nil {
			return nil, fmt.Errorf("invalid task %s: %w", entry.Name(), err)
		}

		tasks = append(tasks, t)
	}

	if len(tasks) == 0 {
		return nil, fmt.Errorf("no task files found in %s", dir)
	}

	return &Registry{tasks: tasks}, nil
}

// All returns all loaded tasks.
func (r *Registry) All() []Task {
	return r.tasks
}

// ByID returns a single task by ID.
func (r *Registry) ByID(id string) (Task, bool) {
	for _, t := range r.tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// ByCategory returns tasks matching the given category.
func (r *Registry) ByCategory(category string) []Task {
	var out []Task
	for _, t := range r.tasks {
		if t.Category == category {
			out = append(out, t)
		}
	}
	return out
}

// Count returns the number of loaded tasks.
func (r *Registry) Count() int {
	return len(r.tasks)
}

func validateTask(t Task) error {
	if t.ID == "" {
		return fmt.Errorf("missing id")
	}
	if t.Repo == "" {
		return fmt.Errorf("missing repo")
	}
	if t.BaseCommit == "" {
		return fmt.Errorf("missing base_commit")
	}
	if t.ProblemStatement == "" {
		return fmt.Errorf("missing problem_statement")
	}
	if t.Validation.TestCmd == "" {
		return fmt.Errorf("missing validation.test_cmd")
	}
	if len(t.Validation.FailToPass) == 0 {
		return fmt.Errorf("missing validation.fail_to_pass")
	}
	if t.MaxTurns <= 0 {
		t.MaxTurns = 30
	}
	if t.MaxBudgetUSD <= 0 {
		t.MaxBudgetUSD = 2.0
	}
	return nil
}
