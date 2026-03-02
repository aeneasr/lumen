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

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aeneasr/agent-index/internal/config"
	"github.com/aeneasr/agent-index/internal/embedder"
	"github.com/aeneasr/agent-index/internal/index"
	"github.com/aeneasr/agent-index/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stdioCmd)
}

var stdioCmd = &cobra.Command{
	Use:   "stdio",
	Short: "Start the MCP server on stdin/stdout",
	RunE:  runStdio,
}

// --- Tool input/output types ---

// SemanticSearchInput defines the parameters for the semantic_search tool.
type SemanticSearchInput struct {
	Query        string   `json:"query" jsonschema:"Natural language search query"`
	Path         string   `json:"path" jsonschema:"Absolute path to the project root"`
	Limit        int      `json:"limit,omitempty" jsonschema:"Max results to return, default 20"`
	MinScore     *float64 `json:"min_score,omitempty" jsonschema:"Minimum score threshold (-1 to 1). Results below this score are excluded. Default 0.5. Use -1 to return all results."`
	ForceReindex bool     `json:"force_reindex,omitempty" jsonschema:"Force full re-index before searching"`
}

// SearchResultItem represents a single search result returned to the caller.
type SearchResultItem struct {
	FilePath  string  `json:"file_path"`
	Symbol    string  `json:"symbol"`
	Kind      string  `json:"kind"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float32 `json:"score"`
	Content   string  `json:"content,omitempty"`
}

// SemanticSearchOutput is the structured output of the semantic_search tool.
type SemanticSearchOutput struct {
	Results      []SearchResultItem `json:"results"`
	Reindexed    bool               `json:"reindexed"`
	IndexedFiles int                `json:"indexed_files,omitempty"`
}

// IndexStatusInput defines the parameters for the index_status tool.
type IndexStatusInput struct {
	Path string `json:"path" jsonschema:"Absolute path to the project root"`
}

// IndexStatusOutput is the structured output of the index_status tool.
type IndexStatusOutput struct {
	ProjectPath    string `json:"project_path"`
	TotalFiles     int    `json:"total_files"`
	IndexedFiles   int    `json:"indexed_files"`
	TotalChunks    int    `json:"total_chunks"`
	LastIndexedAt  string `json:"last_indexed_at"`
	EmbeddingModel string `json:"embedding_model"`
	Stale          bool   `json:"stale"`
}

// --- indexerCache ---

// indexerCache manages one *index.Indexer per project path, creating them
// lazily with a shared embedder.
type indexerCache struct {
	mu       sync.RWMutex
	cache    map[string]*index.Indexer
	embedder embedder.Embedder
	model    string
	cfg      config.Config
}

// getOrCreate returns an existing Indexer for the given project path, or
// creates a new one backed by a SQLite database in the XDG data directory.
func (ic *indexerCache) getOrCreate(projectPath string) (*index.Indexer, error) {
	// Fast path: read lock for already-cached indexers.
	ic.mu.RLock()
	if ic.cache != nil {
		if idx, ok := ic.cache[projectPath]; ok {
			ic.mu.RUnlock()
			return idx, nil
		}
	}
	ic.mu.RUnlock()

	// Slow path: acquire write lock to create.
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.cache == nil {
		ic.cache = make(map[string]*index.Indexer)
	}
	// Double-check: another goroutine may have created it while we waited.
	if idx, ok := ic.cache[projectPath]; ok {
		return idx, nil
	}

	dbPath := config.DBPathForProject(projectPath, ic.model)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	idx, err := index.NewIndexer(dbPath, ic.embedder, ic.cfg.MaxChunkTokens)
	if err != nil {
		return nil, fmt.Errorf("create indexer: %w", err)
	}

	ic.cache[projectPath] = idx
	return idx, nil
}

// handleSemanticSearch is the tool handler for the semantic_search tool.
// Uses Out=any so the SDK does not set StructuredContent — the LLM sees
// only the plaintext in Content.
func (ic *indexerCache) handleSemanticSearch(ctx context.Context, req *mcp.CallToolRequest, input SemanticSearchInput) (*mcp.CallToolResult, any, error) {
	if err := validateSearchInput(&input); err != nil {
		return nil, nil, err
	}

	idx, err := ic.getOrCreate(input.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("get indexer: %w", err)
	}

	progress := buildProgressFunc(ctx, req)

	out, err := ic.ensureIndexed(ctx, idx, input, progress)
	if err != nil {
		return nil, nil, err
	}

	queryVec, err := ic.embedQuery(ctx, input.Query)
	if err != nil {
		return nil, nil, err
	}

	maxDistance := computeMaxDistance(input.MinScore)

	results, err := idx.Search(ctx, input.Path, queryVec, input.Limit, maxDistance)
	if err != nil {
		return nil, nil, fmt.Errorf("search: %w", err)
	}

	out.Results = make([]SearchResultItem, len(results))
	snippets := extractSnippets(input.Path, results)
	for i, r := range results {
		out.Results[i] = SearchResultItem{
			FilePath:  r.FilePath,
			Symbol:    r.Symbol,
			Kind:      r.Kind,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Score:     float32(1.0 - r.Distance),
			Content:   snippets[i],
		}
	}

	text := formatSearchResults(input.Path, out)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func validateSearchInput(input *SemanticSearchInput) error {
	if input.Path == "" {
		return fmt.Errorf("path is required")
	}
	if input.Query == "" {
		return fmt.Errorf("query is required")
	}
	if input.Limit <= 0 {
		input.Limit = 20
	}
	return nil
}

func buildProgressFunc(ctx context.Context, req *mcp.CallToolRequest) index.ProgressFunc {
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	return func(current, total int, message string) {
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      float64(current),
			Total:         float64(total),
			Message:       message,
		})
	}
}

func (ic *indexerCache) ensureIndexed(ctx context.Context, idx *index.Indexer, input SemanticSearchInput, progress index.ProgressFunc) (SemanticSearchOutput, error) {
	out := SemanticSearchOutput{}
	if input.ForceReindex {
		stats, err := idx.Index(ctx, input.Path, true, progress)
		if err != nil {
			return out, fmt.Errorf("force reindex: %w", err)
		}
		out.Reindexed = true
		out.IndexedFiles = stats.IndexedFiles
		return out, nil
	}

	reindexed, stats, err := idx.EnsureFresh(ctx, input.Path, progress)
	if err != nil {
		return out, fmt.Errorf("ensure fresh: %w", err)
	}
	out.Reindexed = reindexed
	if reindexed {
		out.IndexedFiles = stats.IndexedFiles
	}
	return out, nil
}

func (ic *indexerCache) embedQuery(ctx context.Context, query string) ([]float32, error) {
	vecs, err := ic.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedder returned no vectors")
	}
	return vecs[0], nil
}

func computeMaxDistance(minScore *float64) float64 {
	if minScore == nil {
		return 0.5 // Default: 0.5 min_score
	}
	if *minScore > -1 {
		return 1.0 - *minScore
	}
	return 0 // -1 means no filter
}

// handleIndexStatus is the tool handler for the index_status tool.
// Uses Out=any so the SDK does not set StructuredContent.
func (ic *indexerCache) handleIndexStatus(_ context.Context, _ *mcp.CallToolRequest, input IndexStatusInput) (*mcp.CallToolResult, any, error) {
	if input.Path == "" {
		return nil, nil, fmt.Errorf("path is required")
	}

	idx, err := ic.getOrCreate(input.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("get indexer: %w", err)
	}

	info, err := idx.Status(input.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("get status: %w", err)
	}

	out := IndexStatusOutput{
		ProjectPath:    info.ProjectPath,
		TotalFiles:     info.TotalFiles,
		IndexedFiles:   info.IndexedFiles,
		TotalChunks:    info.TotalChunks,
		LastIndexedAt:  info.LastIndexedAt,
		EmbeddingModel: info.EmbeddingModel,
	}

	fresh, err := idx.IsFresh(input.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("check freshness: %w", err)
	}
	out.Stale = !fresh

	text := formatIndexStatus(out)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

// extractSnippets reads source files and extracts the code at the line ranges
// specified by each search result. Returns one string per result (empty on read error).
func extractSnippets(projectPath string, results []store.SearchResult) []string {
	snippets := make([]string, len(results))
	filesByPath := groupResultsByFile(results)

	for filePath, refs := range filesByPath {
		lines := readFileLines(projectPath, filePath)
		extractForFile(snippets, lines, refs)
	}

	return snippets
}

type resultRef struct {
	idx       int
	startLine int
	endLine   int
}

func groupResultsByFile(results []store.SearchResult) map[string][]resultRef {
	byFile := make(map[string][]resultRef)
	for i, r := range results {
		byFile[r.FilePath] = append(byFile[r.FilePath], resultRef{i, r.StartLine, r.EndLine})
	}
	return byFile
}

func readFileLines(projectPath, filePath string) []string {
	absPath := filepath.Join(projectPath, filePath)
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	// Find max line first to limit reads
	maxLine := 0
	scanner := bufio.NewScanner(f)
	for i := 1; scanner.Scan(); i++ {
		maxLine = i
	}

	// Re-open and read up to maxLine
	f2, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f2.Close() }()

	var lines []string
	scanner = bufio.NewScanner(f2)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) >= maxLine {
			break
		}
	}
	return lines
}

func extractForFile(snippets []string, lines []string, refs []resultRef) {
	for _, ref := range refs {
		start, end := normalizeLineRange(ref.startLine, ref.endLine, len(lines))
		if start >= end {
			continue
		}
		snippets[ref.idx] = strings.Join(lines[start:end], "\n")
	}
}

func normalizeLineRange(startLine, endLine, totalLines int) (int, int) {
	start := max(startLine-1, 0)
	end := min(endLine, totalLines)
	return start, end
}

// formatSearchResults builds a compact plaintext representation of search
// results for LLM consumption. File paths are shown relative to the project root.
func formatSearchResults(projectPath string, out SemanticSearchOutput) string {
	if len(out.Results) == 0 {
		var b strings.Builder
		b.WriteString("No results found.")
		if out.Reindexed {
			fmt.Fprintf(&b, " (indexed %d files)", out.IndexedFiles)
		}
		return b.String()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d results", len(out.Results))
	if out.Reindexed {
		fmt.Fprintf(&b, " (indexed %d files)", out.IndexedFiles)
	}
	b.WriteString(":\n")

	for _, r := range out.Results {
		rel, err := filepath.Rel(projectPath, r.FilePath)
		if err != nil {
			rel = r.FilePath
		}
		fmt.Fprintf(&b, "\n── %s:%d-%d  %s (%s) [%.2f] ──\n", rel, r.StartLine, r.EndLine, r.Symbol, r.Kind, r.Score)
		if r.Content != "" {
			b.WriteString(r.Content)
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// formatIndexStatus builds a compact plaintext representation of index status.
func formatIndexStatus(out IndexStatusOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Index: %s\n", out.ProjectPath)
	fmt.Fprintf(&b, "Files: %d | Indexed: %d | Chunks: %d | Model: %s\n", out.TotalFiles, out.IndexedFiles, out.TotalChunks, out.EmbeddingModel)
	stale := "no"
	if out.Stale {
		stale = "yes"
	}
	lastIndexed := out.LastIndexedAt
	if lastIndexed == "" {
		lastIndexed = "never"
	}
	fmt.Fprintf(&b, "Last indexed: %s | Stale: %s", lastIndexed, stale)
	return b.String()
}

func runStdio(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	emb, err := newEmbedder(cfg)
	if err != nil {
		return fmt.Errorf("create embedder: %w", err)
	}

	indexers := &indexerCache{embedder: emb, model: cfg.Model, cfg: cfg}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "agent-index",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "semantic_search",
		Description: `Search indexed codebase using natural language. Returns file paths and line ranges of semantically matching code chunks. Auto-indexes if the index is stale or empty.

Use this tool for ANY code or file search task, including:
- Finding where functionality is implemented (e.g. "rate limiter", "authentication handler", "database connection pool")
- Locating code and docs related to a concept, feature, or domain term
- Discovering how a system works or where logic lives
- Finding relevant code before making changes

This tool understands code and text semantics. It finds results that keyword search (grep) would miss because it matches meaning, not just text. Prefer this over grep/glob for code discovery.

If a search returns no results, retry with a lower min_score (e.g. 0.0 or -1) before trying a different query — the embedding model may score the match below the default threshold.`,
	}, indexers.handleSemanticSearch)

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_status",
		Description: `Check the indexing status of a project. Shows total files, indexed chunks, and embedding model.

Use this to verify a project is indexed before searching, or to check if the index is up to date.`,
	}, indexers.handleIndexStatus)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}
