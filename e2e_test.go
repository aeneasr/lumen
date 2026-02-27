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
	"strings"
	"testing"
	"time"

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
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				t.Fatalf("semantic_search returned error: %s", tc.Text)
			}
		}
		t.Fatalf("semantic_search returned error (no text content)")
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

// callSearchRaw calls semantic_search and returns the raw CallToolResult (for error testing).
func callSearchRaw(t *testing.T, session *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "semantic_search",
		Arguments: mustJSON(t, args),
	})
	if err != nil {
		t.Fatalf("CallTool semantic_search failed: %v", err)
	}
	return result
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

// resultSymbols extracts symbol names from search results.
func resultSymbols(results []SearchResultItem) []string {
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Symbol
	}
	return names
}

// findResult returns the first result matching the given symbol name, or nil.
func findResult(results []SearchResultItem, symbol string) *SearchResultItem {
	for i := range results {
		if results[i].Symbol == symbol {
			return &results[i]
		}
	}
	return nil
}

// rankOf returns the 0-based index of the first result matching symbol, or -1.
func rankOf(results []SearchResultItem, symbol string) int {
	for i, r := range results {
		if r.Symbol == symbol {
			return i
		}
	}
	return -1
}

// copyDir copies src directory contents to dst.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	cmd := exec.Command("cp", "-r", src+"/.", dst)
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to copy %s to %s: %v", src, dst, err)
	}
}

// validChunkKinds is the set of chunk kinds produced by the Go AST chunker.
var validChunkKinds = map[string]bool{
	"function":  true,
	"method":    true,
	"type":      true,
	"interface": true,
	"const":     true,
	"var":       true,
	"package":   true,
}

// --- Tests ---

func TestE2E_ToolDiscovery(t *testing.T) {
	session := startServer(t)

	ctx := context.Background()
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	tools := make(map[string]*mcp.Tool)
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}

	for _, name := range []string{"semantic_search", "index_status"} {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("expected tool %q not found", name)
			continue
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", name)
		}

		// InputSchema is any; unmarshal to check properties.
		schemaBytes, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Errorf("tool %q: failed to marshal InputSchema: %v", name, err)
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(schemaBytes, &schema); err != nil {
			t.Errorf("tool %q: failed to unmarshal InputSchema: %v", name, err)
			continue
		}
		props, _ := schema["properties"].(map[string]any)
		if props == nil {
			t.Errorf("tool %q: InputSchema has no properties", name)
			continue
		}
		if _, ok := props["path"]; !ok {
			t.Errorf("tool %q: missing 'path' property in schema", name)
		}
	}

	// Verify semantic_search has query in its schema.
	if ss, ok := tools["semantic_search"]; ok {
		schemaBytes, _ := json.Marshal(ss.InputSchema)
		var schema map[string]any
		_ = json.Unmarshal(schemaBytes, &schema)
		props, _ := schema["properties"].(map[string]any)
		if _, hasQuery := props["query"]; !hasQuery {
			t.Error("semantic_search missing 'query' property in schema")
		}
	}
}

func TestE2E_IndexAndSearchResults(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	out := callSearch(t, session, map[string]any{
		"query": "authentication token validation",
		"path":  projectPath,
		"limit": 5,
	})

	if !out.Reindexed {
		t.Error("expected Reindexed=true on first search")
	}
	if out.IndexedFiles != 5 {
		t.Errorf("expected IndexedFiles=5, got %d", out.IndexedFiles)
	}

	// Limit respected.
	if len(out.Results) > 5 {
		t.Errorf("expected at most 5 results, got %d", len(out.Results))
	}
	if len(out.Results) == 0 {
		t.Fatal("expected at least one search result")
	}

	// Validate every result has well-formed fields.
	for i, r := range out.Results {
		if r.FilePath == "" || !strings.HasSuffix(r.FilePath, ".go") {
			t.Errorf("result[%d]: FilePath should be non-empty and end in .go, got %q", i, r.FilePath)
		}
		if r.Symbol == "" {
			t.Errorf("result[%d]: Symbol should be non-empty", i)
		}
		if !validChunkKinds[r.Kind] {
			t.Errorf("result[%d]: Kind %q is not a valid chunk kind", i, r.Kind)
		}
		if r.StartLine <= 0 {
			t.Errorf("result[%d]: StartLine should be > 0, got %d", i, r.StartLine)
		}
		if r.EndLine < r.StartLine {
			t.Errorf("result[%d]: EndLine (%d) should be >= StartLine (%d)", i, r.EndLine, r.StartLine)
		}
		if r.Score <= 0 || r.Score > 1 {
			t.Errorf("result[%d]: Score should be in (0, 1], got %f", i, r.Score)
		}
	}

	// Results sorted by score descending.
	for i := 1; i < len(out.Results); i++ {
		if out.Results[i].Score > out.Results[i-1].Score {
			t.Errorf("results not sorted by score descending: result[%d].Score=%f > result[%d].Score=%f",
				i, out.Results[i].Score, i-1, out.Results[i-1].Score)
		}
	}

	// Semantic relevance: ValidateToken should appear (it's literally about token validation).
	if findResult(out.Results, "ValidateToken") == nil {
		t.Errorf("expected ValidateToken in results for 'authentication token validation', got: %v", resultSymbols(out.Results))
	}
}

func TestE2E_SearchRelevanceRanking(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	// HandleHealth should rank higher than ValidateToken for an HTTP handler query.
	// Use a high limit to ensure both symbols appear.
	out := callSearch(t, session, map[string]any{
		"query": "HTTP request handler for health check endpoint",
		"path":  projectPath,
		"limit": 50,
	})
	healthRank := rankOf(out.Results, "HandleHealth")
	tokenRank := rankOf(out.Results, "ValidateToken")
	if healthRank == -1 {
		t.Fatalf("HandleHealth not found in results: %v", resultSymbols(out.Results))
	}
	if tokenRank == -1 {
		t.Fatalf("ValidateToken not found in results: %v", resultSymbols(out.Results))
	}
	if healthRank >= tokenRank {
		t.Errorf("expected HandleHealth (rank %d) to rank higher than ValidateToken (rank %d) for HTTP handler query",
			healthRank, tokenRank)
	}

	// QueryUsers should rank higher than HandleHealth for a database query.
	out2 := callSearch(t, session, map[string]any{
		"query": "database query pagination",
		"path":  projectPath,
		"limit": 50,
	})
	queryRank := rankOf(out2.Results, "QueryUsers")
	handleRank := rankOf(out2.Results, "HandleHealth")
	if queryRank == -1 {
		t.Fatalf("QueryUsers not found in results: %v", resultSymbols(out2.Results))
	}
	if handleRank == -1 {
		t.Fatalf("HandleHealth not found in results: %v", resultSymbols(out2.Results))
	}
	if queryRank >= handleRank {
		t.Errorf("expected QueryUsers (rank %d) to rank higher than HandleHealth (rank %d) for database query",
			queryRank, handleRank)
	}
}

func TestE2E_LimitParameter(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	// limit=1 should return exactly 1.
	out1 := callSearch(t, session, map[string]any{
		"query": "user",
		"path":  projectPath,
		"limit": 1,
	})
	if len(out1.Results) != 1 {
		t.Errorf("limit=1: expected exactly 1 result, got %d", len(out1.Results))
	}

	// limit=3 should return at most 3.
	out3 := callSearch(t, session, map[string]any{
		"query": "user",
		"path":  projectPath,
		"limit": 3,
	})
	if len(out3.Results) > 3 {
		t.Errorf("limit=3: expected at most 3 results, got %d", len(out3.Results))
	}

	// No limit (omitted) should return results (default 10 kicks in).
	outDefault := callSearch(t, session, map[string]any{
		"query": "user",
		"path":  projectPath,
	})
	if len(outDefault.Results) == 0 {
		t.Error("no limit: expected results with default limit")
	}
}

func TestE2E_IncrementalIndex(t *testing.T) {
	session := startServer(t)

	tmpDir := t.TempDir()
	copyDir(t, sampleProjectPath(t), tmpDir)

	// First search triggers indexing.
	out1 := callSearch(t, session, map[string]any{
		"query": "authentication",
		"path":  tmpDir,
	})
	if !out1.Reindexed {
		t.Error("first search: expected Reindexed=true")
	}

	// Second search with no changes should skip re-indexing.
	out2 := callSearch(t, session, map[string]any{
		"query": "authentication",
		"path":  tmpDir,
	})
	if out2.Reindexed {
		t.Error("second search (no changes): expected Reindexed=false")
	}

	// Add a new file.
	newFile := filepath.Join(tmpDir, "shutdown.go")
	code := `package project

import "fmt"

// GracefulShutdown performs a graceful shutdown of all active connections.
func GracefulShutdown(timeout int) error {
	fmt.Printf("shutting down with timeout %d\n", timeout)
	return nil
}
`
	if err := os.WriteFile(newFile, []byte(code), 0o644); err != nil {
		t.Fatalf("failed to write new file: %v", err)
	}

	outAdd := callSearch(t, session, map[string]any{
		"query": "graceful shutdown",
		"path":  tmpDir,
	})
	if !outAdd.Reindexed {
		t.Error("after adding file: expected Reindexed=true")
	}
	if findResult(outAdd.Results, "GracefulShutdown") == nil {
		t.Errorf("expected GracefulShutdown in results after adding file, got: %v", resultSymbols(outAdd.Results))
	}

	// Modify an existing file: replace ValidateToken with VerifyCredentials.
	authFile := filepath.Join(tmpDir, "auth.go")
	modifiedAuth := `package project

import (
	"errors"
)

// VerifyCredentials checks whether user credentials are valid.
func VerifyCredentials(username, password string) error {
	if username == "" {
		return errors.New("empty username")
	}
	if password == "" {
		return errors.New("empty password")
	}
	return nil
}
`
	if err := os.WriteFile(authFile, []byte(modifiedAuth), 0o644); err != nil {
		t.Fatalf("failed to rewrite auth.go: %v", err)
	}

	outMod := callSearch(t, session, map[string]any{
		"query": "verify credentials",
		"path":  tmpDir,
	})
	if !outMod.Reindexed {
		t.Error("after modifying file: expected Reindexed=true")
	}
	if findResult(outMod.Results, "VerifyCredentials") == nil {
		t.Errorf("expected VerifyCredentials in results after modification, got: %v", resultSymbols(outMod.Results))
	}
	if findResult(outMod.Results, "ValidateToken") != nil {
		t.Error("ValidateToken should not appear after being replaced")
	}

	// Delete a file.
	if err := os.Remove(filepath.Join(tmpDir, "database.go")); err != nil {
		t.Fatalf("failed to delete database.go: %v", err)
	}

	outDel := callSearch(t, session, map[string]any{
		"query": "database query",
		"path":  tmpDir,
	})
	if !outDel.Reindexed {
		t.Error("after deleting file: expected Reindexed=true")
	}
	if findResult(outDel.Results, "QueryUsers") != nil {
		t.Error("QueryUsers should not appear after deleting database.go")
	}
}

func TestE2E_IndexStatus(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	// Status before any indexing.
	statusBefore := callStatus(t, session, map[string]any{
		"path": projectPath,
	})
	if statusBefore.TotalFiles != 0 {
		t.Errorf("before indexing: expected TotalFiles=0, got %d", statusBefore.TotalFiles)
	}
	if statusBefore.TotalChunks != 0 {
		t.Errorf("before indexing: expected TotalChunks=0, got %d", statusBefore.TotalChunks)
	}

	// Trigger indexing via search.
	callSearch(t, session, map[string]any{
		"query": "anything",
		"path":  projectPath,
	})

	// Status after indexing.
	status := callStatus(t, session, map[string]any{
		"path": projectPath,
	})
	if status.TotalFiles != 5 {
		t.Errorf("expected TotalFiles=5, got %d", status.TotalFiles)
	}
	if status.IndexedFiles != 5 {
		t.Errorf("expected IndexedFiles=5, got %d", status.IndexedFiles)
	}
	if status.TotalChunks <= 15 {
		t.Errorf("expected TotalChunks > 15 (fixture has ~21 symbols), got %d", status.TotalChunks)
	}
	if status.EmbeddingModel != "all-minilm" {
		t.Errorf("expected EmbeddingModel=all-minilm, got %q", status.EmbeddingModel)
	}
	if status.ProjectPath != projectPath {
		t.Errorf("expected ProjectPath=%s, got %s", projectPath, status.ProjectPath)
	}
	if status.LastIndexedAt == "" {
		t.Error("expected LastIndexedAt to be non-empty")
	} else {
		ts, err := time.Parse(time.RFC3339, status.LastIndexedAt)
		if err != nil {
			t.Errorf("LastIndexedAt is not valid RFC3339: %q", status.LastIndexedAt)
		} else if time.Since(ts) > 60*time.Second {
			t.Errorf("LastIndexedAt is too old: %s (more than 60s ago)", status.LastIndexedAt)
		}
	}
}

func TestE2E_ForceReindex(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	// Normal search triggers indexing.
	out1 := callSearch(t, session, map[string]any{
		"query": "config",
		"path":  projectPath,
	})
	if !out1.Reindexed {
		t.Error("first search: expected Reindexed=true")
	}

	// Second search (no changes) should skip.
	out2 := callSearch(t, session, map[string]any{
		"query": "config",
		"path":  projectPath,
	})
	if out2.Reindexed {
		t.Error("second search (no changes): expected Reindexed=false")
	}

	// Force reindex should re-index even with no changes.
	out3 := callSearch(t, session, map[string]any{
		"query":         "config",
		"path":          projectPath,
		"force_reindex": true,
	})
	if !out3.Reindexed {
		t.Error("force_reindex: expected Reindexed=true")
	}
	if out3.IndexedFiles != 5 {
		t.Errorf("force_reindex: expected IndexedFiles=5, got %d", out3.IndexedFiles)
	}
}

func TestE2E_ErrorHandling(t *testing.T) {
	session := startServer(t)

	// Non-existent project path should return IsError=true.
	result := callSearchRaw(t, session, map[string]any{
		"query": "test",
		"path":  "/nonexistent/path/that/does/not/exist",
	})
	if !result.IsError {
		t.Error("expected IsError=true for non-existent project path")
	}

	// Empty project directory (no .go files) should return 0 results, not an error.
	emptyDir := t.TempDir()
	out := callSearch(t, session, map[string]any{
		"query": "anything",
		"path":  emptyDir,
	})
	if len(out.Results) != 0 {
		t.Errorf("expected 0 results for empty project, got %d", len(out.Results))
	}
}
