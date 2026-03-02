// Copyright 2026 Aeneas Rekkas
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aeneasr/lumen/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Local copies of tool I/O types for black-box E2E testing.
// These mirror cmd.SemanticSearchInput etc. but are intentionally
// decoupled so the E2E tests don't depend on cmd internals.

type semanticSearchOutput struct {
	Results      []searchResultItem `json:"results"`
	Reindexed    bool               `json:"reindexed"`
	IndexedFiles int                `json:"indexed_files,omitempty"`
}

type searchResultItem struct {
	FilePath  string  `json:"file_path"`
	Symbol    string  `json:"symbol"`
	Kind      string  `json:"kind"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float32 `json:"score"`
	Content   string  `json:"content,omitempty"`
}

type indexStatusOutput struct {
	ProjectPath    string `json:"project_path"`
	TotalFiles     int    `json:"total_files"`
	IndexedFiles   int    `json:"indexed_files"`
	TotalChunks    int    `json:"total_chunks"`
	LastIndexedAt  string `json:"last_indexed_at"`
	EmbeddingModel string `json:"embedding_model"`
	Stale          bool   `json:"stale"`
}

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
	ollamaHost := config.EnvOrDefault("OLLAMA_HOST", "http://localhost:11434")
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
	return startServerWithOpts(t, nil)
}

// startServerWithOpts launches the MCP server with custom client options.
func startServerWithOpts(t *testing.T, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()

	dataHome := t.TempDir()
	ollamaHost := config.EnvOrDefault("OLLAMA_HOST", "http://localhost:11434")

	cmd := exec.Command(serverBinary, "stdio")
	cmd.Env = []string{
		"OLLAMA_HOST=" + ollamaHost,
		"LUMEN_EMBED_MODEL=all-minilm",
		"XDG_DATA_HOME=" + dataHome,
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}

	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "e2e-test-client",
		Version: "0.1.0",
	}, opts)

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

// getTextContent extracts the text from the first TextContent block in a CallToolResult.
func getTextContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected at least one Content block")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return tc.Text
}

// headerRe matches XML result opening tags like:
// <search:result filename="auth.go" line-start="10" line-end="19" symbol="ValidateToken" kind="function" score="0.66">
var headerRe = regexp.MustCompile(`^<search:result filename="([^"]+)" line-start="(\d+)" line-end="(\d+)" symbol="([^"]+)" kind="([^"]+)" score="(-?\d+\.\d+)">$`)

// parseSearchText parses the XML-tagged output of semantic_search into a semanticSearchOutput.
func parseSearchText(t *testing.T, text string) semanticSearchOutput {
	t.Helper()

	var out semanticSearchOutput

	// Parse "(indexed M files)" first — present in both "Found N results" and "No results found." output.
	if strings.Contains(text, "(indexed") {
		re := regexp.MustCompile(`\(indexed (\d+) files\)`)
		if m := re.FindStringSubmatch(text); m != nil {
			out.IndexedFiles, _ = strconv.Atoi(m[1])
			out.Reindexed = true
		}
	}
	if strings.HasPrefix(text, "No results found") {
		return out
	}

	// Find all header line positions, then extract content between them.
	lines := strings.Split(text, "\n")
	type headerMatch struct {
		lineIdx   int
		filePath  string
		symbol    string
		kind      string
		startLine int
		endLine   int
		score     float32
	}
	var headers []headerMatch
	for i, line := range lines {
		m := headerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		startLine, _ := strconv.Atoi(m[2])
		endLine, _ := strconv.Atoi(m[3])
		score, _ := strconv.ParseFloat(m[6], 32)
		headers = append(headers, headerMatch{i, m[1], m[4], m[5], startLine, endLine, float32(score)})
	}

	for hi, h := range headers {
		// Content runs from header line+1 to the line before the next header or closing tag (or end).
		contentEnd := len(lines)
		if hi+1 < len(headers) {
			contentEnd = headers[hi+1].lineIdx
		}
		var contentLines []string
		for j := h.lineIdx + 1; j < contentEnd; j++ {
			if lines[j] == "</search:result>" {
				break
			}
			contentLines = append(contentLines, lines[j])
		}
		content := strings.TrimSpace(strings.Join(contentLines, "\n"))

		out.Results = append(out.Results, searchResultItem{
			FilePath:  h.filePath,
			Symbol:    h.symbol,
			Kind:      h.kind,
			StartLine: h.startLine,
			EndLine:   h.endLine,
			Score:     h.score,
			Content:   content,
		})
	}

	return out
}

// callSearch calls the semantic_search tool and returns the parsed output.
func callSearch(t *testing.T, session *mcp.ClientSession, args map[string]any) semanticSearchOutput {
	t.Helper()
	result := callSearchRaw(t, session, args)
	if result.IsError {
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				t.Fatalf("semantic_search returned error: %s", tc.Text)
			}
		}
		t.Fatalf("semantic_search returned error (no text content)")
	}
	text := getTextContent(t, result)
	return parseSearchText(t, text)
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

// parseStatusText parses the plaintext output of index_status.
func parseStatusText(t *testing.T, text string) indexStatusOutput {
	t.Helper()

	var out indexStatusOutput

	// Parse "Index: <path>"
	if m := regexp.MustCompile(`Index: (.+)`).FindStringSubmatch(text); m != nil {
		out.ProjectPath = m[1]
	}

	// Parse "Files: N | Indexed: N | Chunks: N | Model: ..."
	if m := regexp.MustCompile(`Files: (\d+) \| Indexed: (\d+) \| Chunks: (\d+) \| Model: (.+)`).FindStringSubmatch(text); m != nil {
		out.TotalFiles, _ = strconv.Atoi(m[1])
		out.IndexedFiles, _ = strconv.Atoi(m[2])
		out.TotalChunks, _ = strconv.Atoi(m[3])
		out.EmbeddingModel = m[4]
	}

	// Parse "Last indexed: ... | Stale: yes/no"
	if m := regexp.MustCompile(`Last indexed: (.+?) \| Stale: (\w+)`).FindStringSubmatch(text); m != nil {
		if m[1] != "never" {
			out.LastIndexedAt = m[1]
		}
		out.Stale = m[2] == "yes"
	}

	return out
}

// callStatus calls the index_status tool and returns the parsed output.
func callStatus(t *testing.T, session *mcp.ClientSession, args map[string]any) indexStatusOutput {
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

	text := getTextContent(t, result)
	return parseStatusText(t, text)
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
func resultSymbols(results []searchResultItem) []string {
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Symbol
	}
	return names
}

// findResult returns the first result matching the given symbol name, or nil.
func findResult(results []searchResultItem, symbol string) *searchResultItem {
	for i := range results {
		if results[i].Symbol == symbol {
			return &results[i]
		}
	}
	return nil
}

// rankOf returns the 0-based index of the first result matching symbol, or -1.
func rankOf(results []searchResultItem, symbol string) int {
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

	// Every result should have code content.
	for i, r := range out.Results {
		if r.Content == "" {
			t.Errorf("result[%d] %s: expected non-empty Content (code snippet)", i, r.Symbol)
		}
	}

	// ValidateToken's content should contain the actual function code.
	if vt := findResult(out.Results, "ValidateToken"); vt != nil {
		if !strings.Contains(vt.Content, "func ValidateToken") {
			t.Errorf("ValidateToken content should contain 'func ValidateToken', got: %s", vt.Content[:min(len(vt.Content), 200)])
		}
	}
}

func TestE2E_PlaintextContent(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	// Get raw result to inspect Content (text) alongside StructuredContent.
	result := callSearchRaw(t, session, map[string]any{
		"query": "authentication token validation",
		"path":  projectPath,
		"limit": 3,
	})

	if result.IsError {
		t.Fatalf("unexpected error: %+v", result.Content)
	}

	// Content should have exactly one TextContent block.
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 Content block, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	// Plaintext should contain "Found N results".
	if !strings.Contains(tc.Text, "Found") || !strings.Contains(tc.Text, "results") {
		t.Errorf("plaintext should contain 'Found N results', got: %s", tc.Text[:min(len(tc.Text), 200)])
	}

	// Plaintext should contain actual code.
	if !strings.Contains(tc.Text, "func ") {
		t.Errorf("plaintext should contain code snippets with 'func ', got: %s", tc.Text[:min(len(tc.Text), 500)])
	}

	// No StructuredContent — only plaintext Content for LLM consumption.
	if result.StructuredContent != nil {
		t.Error("expected StructuredContent to be nil (plaintext-only mode)")
	}

	// Verify index_status also returns plaintext.
	callSearch(t, session, map[string]any{"query": "anything", "path": projectPath})
	statusResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "index_status",
		Arguments: mustJSON(t, map[string]any{"path": projectPath}),
	})
	if err != nil {
		t.Fatalf("index_status failed: %v", err)
	}
	if len(statusResult.Content) != 1 {
		t.Fatalf("index_status: expected 1 Content block, got %d", len(statusResult.Content))
	}
	stc, ok := statusResult.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("index_status: expected TextContent, got %T", statusResult.Content[0])
	}
	if !strings.Contains(stc.Text, "Index:") || !strings.Contains(stc.Text, "Files:") {
		t.Errorf("index_status plaintext should contain 'Index:' and 'Files:', got: %s", stc.Text)
	}
}

func TestE2E_SearchRelevanceRanking(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	// HandleHealth should rank higher than ValidateToken for an HTTP handler query.
	// Use min_score=-1 to ensure all results appear regardless of score threshold.
	out := callSearch(t, session, map[string]any{
		"query":     "HTTP request handler for health check endpoint",
		"path":      projectPath,
		"limit":     50,
		"min_score": -1,
	})
	healthRank := rankOf(out.Results, "HandleHealth")
	tokenRank := rankOf(out.Results, "ValidateToken")
	if healthRank == -1 {
		// Show raw text for debugging.
		raw := callSearchRaw(t, session, map[string]any{
			"query":     "HTTP request handler for health check endpoint",
			"path":      projectPath,
			"limit":     50,
			"min_score": -1,
		})
		t.Logf("raw text:\n%s", getTextContent(t, raw))
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
		"query":     "database query pagination",
		"path":      projectPath,
		"limit":     50,
		"min_score": -1,
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

	// limit=1 should return exactly 1. Use min_score=-1 to bypass score filtering.
	out1 := callSearch(t, session, map[string]any{
		"query":     "user",
		"path":      projectPath,
		"limit":     1,
		"min_score": -1,
	})
	if len(out1.Results) != 1 {
		t.Errorf("limit=1: expected exactly 1 result, got %d", len(out1.Results))
	}

	// limit=3 should return at most 3.
	out3 := callSearch(t, session, map[string]any{
		"query":     "user",
		"path":      projectPath,
		"limit":     3,
		"min_score": -1,
	})
	if len(out3.Results) > 3 {
		t.Errorf("limit=3: expected at most 3 results, got %d", len(out3.Results))
	}

	// No limit (omitted) should return results (default 20 kicks in).
	outDefault := callSearch(t, session, map[string]any{
		"query":     "user",
		"path":      projectPath,
		"min_score": -1,
	})
	if len(outDefault.Results) == 0 {
		t.Error("no limit: expected results with default limit")
	}
}

func TestE2E_MinScoreFilter(t *testing.T) {
	session := startServer(t)
	projectPath := sampleProjectPath(t)

	// min_score=-1: get all results regardless of score.
	outAll := callSearch(t, session, map[string]any{
		"query":     "authentication token validation",
		"path":      projectPath,
		"limit":     50,
		"min_score": -1,
	})
	if len(outAll.Results) == 0 {
		t.Fatal("expected results")
	}

	// With a high min_score: should get fewer results.
	outFiltered := callSearch(t, session, map[string]any{
		"query":     "authentication token validation",
		"path":      projectPath,
		"limit":     50,
		"min_score": 0.5,
	})

	if len(outFiltered.Results) >= len(outAll.Results) {
		t.Errorf("min_score=0.5 should filter some results: got %d (unfiltered: %d)",
			len(outFiltered.Results), len(outAll.Results))
	}

	// All filtered results should have score >= 0.5.
	for i, r := range outFiltered.Results {
		if r.Score < 0.5 {
			t.Errorf("result[%d] %s: score %.2f below min_score 0.5", i, r.Symbol, r.Score)
		}
	}

	// The top result should still be present.
	if len(outFiltered.Results) == 0 {
		t.Fatal("min_score=0.5 should still return the best match")
	}
	if outFiltered.Results[0].Symbol != outAll.Results[0].Symbol {
		t.Errorf("top result should be the same: got %s vs %s",
			outFiltered.Results[0].Symbol, outAll.Results[0].Symbol)
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
	if !statusBefore.Stale {
		t.Error("before indexing: expected Stale=true")
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
	if status.TotalChunks <= 10 {
		t.Errorf("expected TotalChunks > 10 (fixture has ~16 symbols, no package chunks), got %d", status.TotalChunks)
	}
	if status.EmbeddingModel != "all-minilm" {
		t.Errorf("expected EmbeddingModel=all-minilm, got %q", status.EmbeddingModel)
	}
	if status.ProjectPath != projectPath {
		t.Errorf("expected ProjectPath=%s, got %s", projectPath, status.ProjectPath)
	}
	if status.Stale {
		t.Error("after indexing: expected Stale=false")
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

func TestE2E_ProgressNotifications(t *testing.T) {
	var mu sync.Mutex
	var notifications []mcp.ProgressNotificationParams

	session := startServerWithOpts(t, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			notifications = append(notifications, *req.Params)
		},
	})
	projectPath := sampleProjectPath(t)

	ctx := context.Background()
	params := &mcp.CallToolParams{
		Name:      "semantic_search",
		Arguments: mustJSON(t, map[string]any{"query": "authentication", "path": projectPath}),
		Meta:      mcp.Meta{"progressToken": "test-progress-1"},
	}

	result, err := session.CallTool(ctx, params)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool returned error: %+v", result.Content)
	}

	mu.Lock()
	got := make([]mcp.ProgressNotificationParams, len(notifications))
	copy(got, notifications)
	mu.Unlock()

	if len(got) == 0 {
		t.Fatal("expected progress notifications, got none")
	}

	// All notifications should carry our progress token.
	for i, n := range got {
		if n.ProgressToken != "test-progress-1" {
			t.Errorf("notification[%d]: expected ProgressToken='test-progress-1', got %v", i, n.ProgressToken)
		}
	}

	// First notification should be "Found N files to index".
	if !strings.Contains(got[0].Message, "Found") || !strings.Contains(got[0].Message, "files to index") {
		t.Errorf("first notification message should contain 'Found ... files to index', got %q", got[0].Message)
	}

	// All notifications must have Total > 0 and Progress <= Total.
	for i, n := range got {
		if n.Total <= 0 {
			t.Errorf("notification[%d]: expected Total > 0, got %f", i, n.Total)
		}
		if n.Progress > n.Total {
			t.Errorf("notification[%d]: Progress (%f) > Total (%f)", i, n.Progress, n.Total)
		}
	}

	// Should have at least one processing notification with file count.
	hasProcessing := false
	for _, n := range got {
		if strings.Contains(n.Message, "Processing file") && strings.Contains(n.Message, "/") {
			hasProcessing = true
			break
		}
	}
	if !hasProcessing {
		t.Error("expected at least one 'Processing file N/M' progress notification")
	}

	// Should have at least one embed notification.
	hasEmbed := false
	for _, n := range got {
		if strings.Contains(n.Message, "Embedded") {
			hasEmbed = true
			break
		}
	}
	if !hasEmbed {
		t.Error("expected at least one 'Embedded' progress notification")
	}

	// Last notification should be the completion message.
	last := got[len(got)-1]
	if !strings.Contains(last.Message, "Indexing complete") {
		t.Errorf("last notification should contain 'Indexing complete', got %q", last.Message)
	}

	// Second search (no changes) should NOT produce progress notifications
	// because no indexing happens.
	mu.Lock()
	notifications = notifications[:0]
	mu.Unlock()

	params2 := &mcp.CallToolParams{
		Name:      "semantic_search",
		Arguments: mustJSON(t, map[string]any{"query": "authentication", "path": projectPath}),
		Meta:      mcp.Meta{"progressToken": "test-progress-2"},
	}

	if _, err := session.CallTool(ctx, params2); err != nil {
		t.Fatalf("second CallTool failed: %v", err)
	}

	mu.Lock()
	got2 := len(notifications)
	mu.Unlock()

	if got2 != 0 {
		t.Errorf("expected 0 progress notifications on cached search, got %d", got2)
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
