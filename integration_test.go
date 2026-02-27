//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/foobar/agent-index-go/internal/embedder"
	"github.com/foobar/agent-index-go/internal/index"
)

func TestIntegration_FullPipeline(t *testing.T) {
	ollamaHost := os.Getenv("OLLAMA_HOST")
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}

	model := os.Getenv("AGENT_INDEX_EMBED_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}

	emb, err := embedder.NewOllama(model, 1024, ollamaHost)
	if err != nil {
		t.Fatal(err)
	}

	projectDir := t.TempDir()
	writeTestFile(t, projectDir, "main.go", `package main

import "fmt"

// Run starts the application server and listens for connections.
func Run(port int) error {
	fmt.Printf("listening on port %d\n", port)
	return nil
}

// Shutdown gracefully stops the server.
func Shutdown() {
	fmt.Println("shutting down")
}
`)
	writeTestFile(t, projectDir, "handler.go", `package main

import "net/http"

// HandleHealth returns 200 OK for health checks.
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// HandleAuth validates authentication tokens.
func HandleAuth(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK)
}
`)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	idx, err := index.NewIndexer(dbPath, emb)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	ctx := context.Background()
	stats, err := idx.Index(ctx, projectDir, false)
	if err != nil {
		t.Fatalf("index failed: %v", err)
	}
	t.Logf("Indexed %d files, %d chunks", stats.IndexedFiles, stats.ChunksCreated)

	queryVecs, err := emb.Embed(ctx, []string{"authentication token validation"})
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}

	results, err := idx.Search(ctx, projectDir, queryVecs[0], 5)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	t.Logf("Search results for 'authentication token validation':")
	for _, r := range results {
		t.Logf("  %s:%d-%d %s %s (distance: %.4f)", r.FilePath, r.StartLine, r.EndLine, r.Kind, r.Symbol, r.Distance)
	}

	if len(results) == 0 {
		t.Fatal("expected at least 1 search result")
	}

	// Test incremental re-index
	writeTestFile(t, projectDir, "main.go", `package main

import "fmt"

// Run starts the application server and listens for connections.
func Run(port int) error {
	fmt.Printf("listening on port %d\n", port)
	return nil
}

// Shutdown gracefully stops the server.
func Shutdown() {
	fmt.Println("shutting down")
}

// Restart restarts the server with new configuration.
func Restart() {
	Shutdown()
	Run(8080)
}
`)

	stats2, err := idx.Index(ctx, projectDir, false)
	if err != nil {
		t.Fatalf("re-index failed: %v", err)
	}
	if stats2.FilesChanged == 0 {
		t.Fatal("expected at least 1 changed file on re-index")
	}
	t.Logf("Re-indexed: %d files changed, %d chunks created", stats2.FilesChanged, stats2.ChunksCreated)
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
