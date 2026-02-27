//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var serverBinary string

func TestMain(m *testing.M) {
	// Build the server binary.
	bin := filepath.Join(os.TempDir(), "agent-index-e2e-test")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build server binary: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(bin)

	// Check Ollama health.
	ollamaHost := envOrDefault("OLLAMA_HOST", "http://localhost:11434")
	resp, err := http.Get(ollamaHost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ollama is unreachable at %s: %v — skipping E2E tests\n", ollamaHost, err)
		os.Exit(1)
	}
	resp.Body.Close()

	serverBinary = bin
	os.Exit(m.Run())
}

// startServer launches the MCP server as a subprocess and returns a connected client session.
func startServer(t *testing.T) *mcp.ClientSession {
	t.Helper()

	dataHome := t.TempDir()
	ollamaHost := envOrDefault("OLLAMA_HOST", "http://localhost:11434")

	cmd := exec.Command(serverBinary)
	cmd.Env = []string{
		"OLLAMA_HOST=" + ollamaHost,
		"AGENT_INDEX_EMBED_MODEL=all-minilm",
		"AGENT_INDEX_EMBED_DIMS=384",
		"XDG_DATA_HOME=" + dataHome,
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}

	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "e2e-test-client",
		Version: "0.1.0",
	}, nil)

	ctx := context.Background()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}

	t.Cleanup(func() {
		session.Close()
	})

	return session
}

// callSearch calls the semantic_search tool and returns the parsed output.
func callSearch(t *testing.T, session *mcp.ClientSession, args map[string]any) SemanticSearchOutput {
	t.Helper()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "semantic_search",
		Arguments: mustJSON(t, args),
	})
	if err != nil {
		t.Fatalf("CallTool semantic_search failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("semantic_search returned error: %+v", result.Content)
	}

	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal StructuredContent: %v", err)
	}

	var out SemanticSearchOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("failed to unmarshal SemanticSearchOutput: %v (raw: %s)", err, string(raw))
	}
	return out
}

// callStatus calls the index_status tool and returns the parsed output.
func callStatus(t *testing.T, session *mcp.ClientSession, args map[string]any) IndexStatusOutput {
	t.Helper()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "index_status",
		Arguments: mustJSON(t, args),
	})
	if err != nil {
		t.Fatalf("CallTool index_status failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("index_status returned error: %+v", result.Content)
	}

	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal StructuredContent: %v", err)
	}

	var out IndexStatusOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("failed to unmarshal IndexStatusOutput: %v (raw: %s)", err, string(raw))
	}
	return out
}

// sampleProjectPath returns the absolute path to the test fixture.
func sampleProjectPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("testdata/sample-project")
	if err != nil {
		t.Fatalf("failed to resolve sample project path: %v", err)
	}
	return p
}

// mustJSON marshals args to json.RawMessage for use as CallToolParams.Arguments.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal args: %v", err)
	}
	return data
}

// --- Tests ---

func TestE2E_ListTools(t *testing.T) {
	session := startServer(t)

	ctx := context.Background()
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	toolNames := make(map[string]bool)
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true
	}

	if !toolNames["semantic_search"] {
		t.Error("expected tool 'semantic_search' not found")
	}
	if !toolNames["index_status"] {
		t.Error("expected tool 'index_status' not found")
	}
}

func TestE2E_IndexAndSearch(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	out := callSearch(t, session, map[string]any{
		"query": "authentication token validation",
		"path":  projectPath,
		"limit": 5,
	})

	if !out.Reindexed {
		t.Error("expected reindexed=true on first search")
	}

	if len(out.Results) == 0 {
		t.Fatal("expected at least one search result")
	}

	// Check that at least one auth function appears in top 3 results.
	authSymbols := map[string]bool{
		"ValidateToken": true,
		"CreateSession": true,
		"RevokeSession": true,
	}
	top := min(3, len(out.Results))
	found := false
	for _, r := range out.Results[:top] {
		if authSymbols[r.Symbol] {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one auth function in top 3 results, got: %+v", out.Results[:top])
	}
}

func TestE2E_SearchScoreRange(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	out := callSearch(t, session, map[string]any{
		"query": "user data model",
		"path":  projectPath,
	})

	if len(out.Results) == 0 {
		t.Fatal("expected at least one search result")
	}

	for _, r := range out.Results {
		if r.Score <= 0 || r.Score > 1 {
			t.Errorf("score out of range (0, 1]: symbol=%s score=%f", r.Symbol, r.Score)
		}
	}
}

func TestE2E_SearchNegative(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	out := callSearch(t, session, map[string]any{
		"query": "kubernetes pod scheduling and container orchestration",
		"path":  projectPath,
	})

	for _, r := range out.Results {
		if r.Score > 0.9 {
			t.Errorf("expected no result with score > 0.9 for unrelated query, got symbol=%s score=%f", r.Symbol, r.Score)
		}
	}
}

func TestE2E_IncrementalUpdate(t *testing.T) {
	session := startServer(t)

	// Copy fixture to a temp dir.
	tmpDir := t.TempDir()
	projectPath := sampleProjectPath(t)
	cpCmd := exec.Command("cp", "-r", projectPath+"/.", tmpDir)
	if err := cpCmd.Run(); err != nil {
		t.Fatalf("failed to copy fixture: %v", err)
	}

	// First search to trigger initial indexing.
	callSearch(t, session, map[string]any{
		"query": "authentication",
		"path":  tmpDir,
	})

	// Add a new file with a GracefulShutdown function.
	newFile := filepath.Join(tmpDir, "shutdown.go")
	code := `package project

import "fmt"

// GracefulShutdown performs a graceful shutdown of all active connections and services.
func GracefulShutdown(timeout int) error {
	fmt.Printf("shutting down gracefully with timeout %d\n", timeout)
	return nil
}
`
	if err := os.WriteFile(newFile, []byte(code), 0o644); err != nil {
		t.Fatalf("failed to write new file: %v", err)
	}

	// Search for the new function.
	out := callSearch(t, session, map[string]any{
		"query": "graceful shutdown",
		"path":  tmpDir,
	})

	found := false
	for _, r := range out.Results {
		if r.Symbol == "GracefulShutdown" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected GracefulShutdown in results, got: %+v", out.Results)
	}
}

func TestE2E_IndexStatus(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	// Trigger indexing via a search.
	callSearch(t, session, map[string]any{
		"query": "anything",
		"path":  projectPath,
	})

	out := callStatus(t, session, map[string]any{
		"path": projectPath,
	})

	if out.TotalFiles != 5 {
		t.Errorf("expected TotalFiles=5, got %d", out.TotalFiles)
	}
	if out.IndexedFiles != 5 {
		t.Errorf("expected IndexedFiles=5, got %d", out.IndexedFiles)
	}
	if out.TotalChunks <= 0 {
		t.Errorf("expected TotalChunks > 0, got %d", out.TotalChunks)
	}
	if out.EmbeddingModel == "" {
		t.Error("expected EmbeddingModel to be non-empty")
	}
	if out.ProjectPath != projectPath {
		t.Errorf("expected ProjectPath=%s, got %s", projectPath, out.ProjectPath)
	}
}

func TestE2E_ForceReindex(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	out := callSearch(t, session, map[string]any{
		"query":         "config",
		"path":          projectPath,
		"force_reindex": true,
	})

	if !out.Reindexed {
		t.Error("expected reindexed=true with force_reindex")
	}
	if out.IndexedFiles != 5 {
		t.Errorf("expected IndexedFiles=5, got %d", out.IndexedFiles)
	}
}

func TestE2E_ErrorHandling(t *testing.T) {
	session := startServer(t)
	ctx := context.Background()

	// Missing path (only query).
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "semantic_search",
		Arguments: mustJSON(t, map[string]any{"query": "test"}),
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true when path is missing")
	}

	// Missing query (only path).
	result, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "semantic_search",
		Arguments: mustJSON(t, map[string]any{"path": "/some/path"}),
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true when query is missing")
	}
}
