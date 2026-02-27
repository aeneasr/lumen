package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// mockEmbedder returns fixed vectors for testing.
type mockEmbedder struct {
	dims      int
	model     string
	callCount int
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	m.callCount++
	vecs := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, m.dims)
		for j := range vec {
			vec[j] = float32(i+1) * 0.1
		}
		vecs[i] = vec
	}
	return vecs, nil
}

func (m *mockEmbedder) Dimensions() int  { return m.dims }
func (m *mockEmbedder) ModelName() string { return m.model }

func TestIndexer_IndexAndSearch(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

import "fmt"

// Hello prints a greeting.
func Hello(name string) {
	fmt.Println("hello", name)
}

// Goodbye prints a farewell.
func Goodbye(name string) {
	fmt.Println("bye", name)
}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	stats, err := idx.Index(context.Background(), projectDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.IndexedFiles == 0 {
		t.Fatal("expected at least 1 indexed file")
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected at least 1 chunk created")
	}

	results, err := idx.Search(context.Background(), projectDir, []float32{0.1, 0.1, 0.1, 0.1}, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
}

func TestIndexer_IncrementalIndex(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	idx.Index(context.Background(), projectDir, false)
	firstCallCount := emb.callCount

	stats, err := idx.Index(context.Background(), projectDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if emb.callCount != firstCallCount {
		t.Fatal("expected no additional embedding calls for unchanged project")
	}
	if stats.ChunksCreated != 0 {
		t.Fatalf("expected 0 chunks created on re-index, got %d", stats.ChunksCreated)
	}
}

func TestIndexer_DetectsModifiedFiles(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	idx.Index(context.Background(), projectDir, false)
	firstCallCount := emb.callCount

	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
func World() {}
`)

	stats, _ := idx.Index(context.Background(), projectDir, false)
	if emb.callCount == firstCallCount {
		t.Fatal("expected additional embedding calls after file change")
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected new chunks after file change")
	}
}

func TestIndexer_ForceReindex(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	idx.Index(context.Background(), projectDir, false)
	firstCallCount := emb.callCount

	stats, _ := idx.Index(context.Background(), projectDir, true)
	if emb.callCount == firstCallCount {
		t.Fatal("expected re-embedding on force=true")
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks on force reindex")
	}
}

func TestIndexer_Status(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	idx.Index(context.Background(), projectDir, false)
	status, err := idx.Status(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.IndexedFiles == 0 {
		t.Fatal("expected indexed files > 0")
	}
	if status.EmbeddingModel != "test-model" {
		t.Fatalf("expected model=test-model, got %s", status.EmbeddingModel)
	}
}

func writeGoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
