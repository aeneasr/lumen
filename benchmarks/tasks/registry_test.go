package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromDir(t *testing.T) {
	dir := t.TempDir()

	task := Task{
		ID:               "test-task-1",
		Source:           "test",
		Repo:             "https://github.com/example/repo",
		BaseCommit:       "abc123",
		ProblemStatement: "Fix the bug",
		Language:         "python",
		Difficulty:       "easy",
		Category:         "bug_fix",
		Validation: Validation{
			TestCmd:    "python -m pytest",
			FailToPass: []string{"test_fix"},
		},
		MaxBudgetUSD: 1.0,
		MaxTurns:     10,
	}

	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test-task-1.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	if reg.Count() != 1 {
		t.Fatalf("expected 1 task, got %d", reg.Count())
	}

	got, ok := reg.ByID("test-task-1")
	if !ok {
		t.Fatal("task test-task-1 not found")
	}
	if got.Repo != task.Repo {
		t.Errorf("repo: got %q, want %q", got.Repo, task.Repo)
	}
	if got.Category != "bug_fix" {
		t.Errorf("category: got %q, want %q", got.Category, "bug_fix")
	}
}

func TestLoadFromDir_Empty(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadFromDir(dir)
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestLoadFromDir_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFromDir(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadFromDir_MissingRequiredFields(t *testing.T) {
	dir := t.TempDir()
	task := Task{ID: "incomplete"}
	data, _ := json.Marshal(task)
	if err := os.WriteFile(filepath.Join(dir, "incomplete.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFromDir(dir)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestByCategory(t *testing.T) {
	dir := t.TempDir()

	cats := []struct{ id, cat string }{
		{"task-bugfix-1", "bug_fix"},
		{"task-bugfix-2", "bug_fix"},
		{"task-feature-1", "feature_add"},
	}
	for _, c := range cats {
		task := Task{
			ID:               c.id,
			Source:           "test",
			Repo:             "https://github.com/example/repo",
			BaseCommit:       "abc123",
			ProblemStatement: "Fix it",
			Category:         c.cat,
			Validation: Validation{
				TestCmd:    "pytest",
				FailToPass: []string{"test"},
			},
			MaxBudgetUSD: 1.0,
			MaxTurns:     10,
		}
		data, _ := json.Marshal(task)
		_ = os.WriteFile(filepath.Join(dir, task.ID+".json"), data, 0o644)
	}

	reg, err := LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	bugs := reg.ByCategory("bug_fix")
	if len(bugs) != 2 {
		t.Errorf("expected 2 bug_fix tasks, got %d", len(bugs))
	}

	features := reg.ByCategory("feature_add")
	if len(features) != 1 {
		t.Errorf("expected 1 feature_add task, got %d", len(features))
	}
}
