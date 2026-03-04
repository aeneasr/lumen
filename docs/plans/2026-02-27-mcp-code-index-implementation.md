# MCP Code Index Server — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to
> implement this plan task-by-task.

**Goal:** Build a Go MCP server that provides semantic code search for Claude
Code by indexing Go source files via AST chunking, Ollama embeddings, and SQLite
vector storage with Merkle tree change detection.

**Architecture:** Monolithic Go binary with internal packages (`chunker`,
`embedder`, `store`, `merkle`, `index`). Runs over stdio as an MCP server using
the official `modelcontextprotocol/go-sdk`. SQLite + sqlite-vec for vector
storage via `mattn/go-sqlite3` (CGO). Ollama for embeddings via
`ollama/ollama/api`.

**Tech Stack:** Go 1.22+, `modelcontextprotocol/go-sdk`, `mattn/go-sqlite3`,
`asg017/sqlite-vec-go-bindings/cgo`, `ollama/ollama/api`, `go/ast`+`go/parser`
stdlib

**Design Doc:** `docs/plans/2026-02-27-mcp-code-index-design.md`

---

### Task 1: Project Scaffolding

**Files:**

- Create: `go.mod`
- Create: `main.go`
- Create: `internal/chunker/chunker.go`
- Create: `internal/embedder/embedder.go`
- Create: `internal/store/store.go`
- Create: `internal/merkle/merkle.go`
- Create: `internal/index/index.go`

**Step 1: Initialize Go module and install dependencies**

```bash
go mod init github.com/ory/agent-index
```

**Step 2: Create shared types file**

Create `internal/types.go`:

```go
package internal

// Chunk represents a semantically meaningful piece of source code.
type Chunk struct {
	ID        string // deterministic: sha256(filePath + symbol + startLine)[:16]
	FilePath  string // relative to project root
	Language  string // "go"
	Symbol    string // "FuncName", "TypeName.MethodName"
	Kind      string // "function", "method", "type", "interface", "const", "var", "package"
	StartLine int
	EndLine   int
	Content   string // raw source text, used for embedding
}

// SearchResult is returned by semantic_search tool.
type SearchResult struct {
	FilePath  string  `json:"file_path"`
	Symbol    string  `json:"symbol"`
	Kind      string  `json:"kind"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float32 `json:"score"`
}
```

**Step 3: Create interface stubs for each internal package**

Create `internal/chunker/chunker.go`:

```go
package chunker

import "github.com/ory/agent-index/internal"

// Chunker splits source files into semantically meaningful chunks.
type Chunker interface {
	Supports(language string) bool
	Chunk(filePath string, content []byte) ([]internal.Chunk, error)
}
```

Create `internal/embedder/embedder.go`:

```go
package embedder

import "context"

// Embedder converts text chunks into vector embeddings.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	ModelName() string
}
```

Create `internal/store/store.go` (stub):

```go
package store
```

Create `internal/merkle/merkle.go` (stub):

```go
package merkle
```

Create `internal/index/index.go` (stub):

```go
package index
```

Create `main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "agent-index: not yet implemented")
	os.Exit(1)
}
```

**Step 4: Install dependencies**

```bash
go get github.com/modelcontextprotocol/go-sdk@latest
go get github.com/mattn/go-sqlite3
go get github.com/asg017/sqlite-vec-go-bindings/cgo
go get github.com/ollama/ollama/api
go mod tidy
```

**Step 5: Verify build**

Run: `go build ./...` Expected: Clean build with no errors.

**Step 6: Commit**

```bash
git add -A
git commit -m "scaffold: init project with module, deps, interface stubs"
```

---

### Task 2: Merkle Tree — Change Detection

**Files:**

- Create: `internal/merkle/merkle.go`
- Create: `internal/merkle/merkle_test.go`

**Step 1: Write the failing test**

Create `internal/merkle/merkle_test.go`:

```go
package merkle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildTree_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	tree, err := BuildTree(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tree.RootHash == "" {
		t.Fatal("expected non-empty root hash for empty dir")
	}
	if len(tree.Files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(tree.Files))
	}
}

func TestBuildTree_SingleFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")

	tree, err := BuildTree(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(tree.Files))
	}
	if _, ok := tree.Files["main.go"]; !ok {
		t.Fatal("expected main.go in files map")
	}
}

func TestBuildTree_SkipsGitAndVendor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	writeFile(t, dir, ".git/config", "git config")
	os.MkdirAll(filepath.Join(dir, "vendor"), 0o755)
	writeFile(t, dir, "vendor/lib.go", "package lib\n")
	os.MkdirAll(filepath.Join(dir, "testdata"), 0o755)
	writeFile(t, dir, "testdata/fixture.go", "package testdata\n")

	tree, err := BuildTree(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 {
		t.Fatalf("expected 1 file (main.go only), got %d: %v", len(tree.Files), tree.Files)
	}
}

func TestDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")

	old, _ := BuildTree(dir, nil)
	cur, _ := BuildTree(dir, nil)
	added, removed, modified := Diff(old, cur)
	if len(added)+len(removed)+len(modified) != 0 {
		t.Fatal("expected no changes")
	}
}

func TestDiff_DetectsModifiedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	old, _ := BuildTree(dir, nil)

	writeFile(t, dir, "main.go", "package main\n\nfunc Hello() {}\n")
	cur, _ := BuildTree(dir, nil)

	added, removed, modified := Diff(old, cur)
	if len(modified) != 1 || modified[0] != "main.go" {
		t.Fatalf("expected modified=[main.go], got added=%v removed=%v modified=%v", added, removed, modified)
	}
}

func TestDiff_DetectsAddedAndRemovedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a\n")
	writeFile(t, dir, "b.go", "package b\n")
	old, _ := BuildTree(dir, nil)

	os.Remove(filepath.Join(dir, "b.go"))
	writeFile(t, dir, "c.go", "package c\n")
	cur, _ := BuildTree(dir, nil)

	added, removed, _ := Diff(old, cur)
	if len(added) != 1 || added[0] != "c.go" {
		t.Fatalf("expected added=[c.go], got %v", added)
	}
	if len(removed) != 1 || removed[0] != "b.go" {
		t.Fatalf("expected removed=[b.go], got %v", removed)
	}
}

func TestBuildTree_OnlyGoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "readme.md", "# readme\n")
	writeFile(t, dir, "data.json", "{}\n")

	tree, err := BuildTree(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 {
		t.Fatalf("expected 1 .go file, got %d: %v", len(tree.Files), tree.Files)
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(abs), 0o755)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/merkle/ -v` Expected: FAIL — `BuildTree` and `Diff` not
defined.

**Step 3: Implement merkle package**

Replace `internal/merkle/merkle.go`:

```go
package merkle

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Tree holds the Merkle tree state for a project directory.
type Tree struct {
	RootHash string            // SHA-256 of the root directory
	Files    map[string]string // relative path → content SHA-256 hash
	Dirs     map[string]string // relative dir path → directory hash
}

// SkipFunc returns true for paths that should be skipped during tree building.
// The path argument is relative to the root directory.
type SkipFunc func(relPath string, isDir bool) bool

// DefaultSkip skips .git, vendor, testdata, node_modules, and non-.go files.
func DefaultSkip(relPath string, isDir bool) bool {
	base := filepath.Base(relPath)
	if isDir {
		switch base {
		case ".git", "vendor", "testdata", "node_modules", "_build":
			return true
		}
		return false
	}
	return !strings.HasSuffix(base, ".go")
}

// BuildTree walks rootDir and computes a Merkle tree.
// If skip is nil, DefaultSkip is used.
func BuildTree(rootDir string, skip SkipFunc) (*Tree, error) {
	if skip == nil {
		skip = DefaultSkip
	}

	tree := &Tree{
		Files: make(map[string]string),
		Dirs:  make(map[string]string),
	}

	// Collect file hashes
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(rootDir, path)
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if skip(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}

		if skip(rel, false) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		tree.Files[rel] = hash
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Build directory hashes bottom-up
	tree.RootHash = buildDirHash(tree.Files)
	return tree, nil
}

// buildDirHash computes a single root hash from all file hashes.
func buildDirHash(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s:%s\n", p, files[p])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Diff compares two trees and returns lists of added, removed, and modified file paths.
func Diff(old, cur *Tree) (added, removed, modified []string) {
	for path, curHash := range cur.Files {
		oldHash, exists := old.Files[path]
		if !exists {
			added = append(added, path)
		} else if oldHash != curHash {
			modified = append(modified, path)
		}
	}
	for path := range old.Files {
		if _, exists := cur.Files[path]; !exists {
			removed = append(removed, path)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(modified)
	return
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/merkle/ -v` Expected: All 6 tests PASS.

**Step 5: Commit**

```bash
git add internal/merkle/
git commit -m "feat: add merkle tree change detection for file hashing and diffing"
```

---

### Task 3: Go AST Chunker

**Files:**

- Modify: `internal/chunker/chunker.go`
- Create: `internal/chunker/goast.go`
- Create: `internal/chunker/goast_test.go`

**Step 1: Write the failing test**

Create `internal/chunker/goast_test.go`:

```go
package chunker

import (
	"testing"
)

const testSource = `// Package example provides test fixtures.
package example

import "fmt"

// Hello prints a greeting.
func Hello(name string) {
	fmt.Println("hello", name)
}

// Greeter defines a greeting interface.
type Greeter interface {
	Greet(name string) string
}

// Server handles requests.
type Server struct {
	Port int
	Host string
}

// Start launches the server.
func (s *Server) Start() error {
	return nil
}

// MaxRetries is the max retry count.
const MaxRetries = 3

// DefaultHost is the default hostname.
var DefaultHost = "localhost"
`

func TestGoASTChunker_Supports(t *testing.T) {
	c := NewGoAST()
	if !c.Supports("go") {
		t.Fatal("expected go to be supported")
	}
	if c.Supports("python") {
		t.Fatal("expected python to not be supported")
	}
}

func TestGoASTChunker_ChunkFunctions(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "function", "Hello")
	if found == nil {
		t.Fatal("expected to find function Hello")
	}
	if found.Language != "go" {
		t.Fatalf("expected language=go, got %s", found.Language)
	}
	if found.Content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestGoASTChunker_ChunkMethods(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "method", "Server.Start")
	if found == nil {
		t.Fatal("expected to find method Server.Start")
	}
}

func TestGoASTChunker_ChunkTypes(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "type", "Server")
	if found == nil {
		t.Fatal("expected to find type Server")
	}
}

func TestGoASTChunker_ChunkInterfaces(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "interface", "Greeter")
	if found == nil {
		t.Fatal("expected to find interface Greeter")
	}
}

func TestGoASTChunker_ChunkConstsAndVars(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	if findChunk(chunks, "const", "MaxRetries") == nil {
		t.Fatal("expected to find const MaxRetries")
	}
	if findChunk(chunks, "var", "DefaultHost") == nil {
		t.Fatal("expected to find var DefaultHost")
	}
}

func TestGoASTChunker_ChunkIncludesDocComment(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "function", "Hello")
	if found == nil {
		t.Fatal("expected function Hello")
	}
	if found.Content == "" {
		t.Fatal("expected non-empty content")
	}
	// The doc comment should be included in the content
	if !containsString(found.Content, "Hello prints a greeting") {
		t.Fatalf("expected doc comment in content, got:\n%s", found.Content)
	}
}

func TestGoASTChunker_ChunkIDsDeterministic(t *testing.T) {
	c := NewGoAST()
	chunks1, _ := c.Chunk("example.go", []byte(testSource))
	chunks2, _ := c.Chunk("example.go", []byte(testSource))

	if len(chunks1) != len(chunks2) {
		t.Fatal("chunk counts differ")
	}
	for i := range chunks1 {
		if chunks1[i].ID != chunks2[i].ID {
			t.Fatalf("chunk %d IDs differ: %s vs %s", i, chunks1[i].ID, chunks2[i].ID)
		}
	}
}

func TestGoASTChunker_PackageDocChunk(t *testing.T) {
	c := NewGoAST()
	chunks, err := c.Chunk("example.go", []byte(testSource))
	if err != nil {
		t.Fatal(err)
	}

	found := findChunk(chunks, "package", "package example")
	if found == nil {
		t.Fatal("expected package doc chunk")
	}
}

func findChunk(chunks []Chunk, kind, symbol string) *Chunk {
	for i := range chunks {
		if chunks[i].Kind == kind && chunks[i].Symbol == symbol {
			return &chunks[i]
		}
	}
	return nil
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/chunker/ -v` Expected: FAIL — `NewGoAST`, `Chunk` type
not defined in this package.

**Step 3: Move Chunk type into chunker package and implement GoAST**

Update `internal/chunker/chunker.go`:

```go
package chunker

// Chunk represents a semantically meaningful piece of source code.
type Chunk struct {
	ID        string // deterministic: sha256(filePath + symbol + startLine)[:16]
	FilePath  string // relative to project root
	Language  string // "go"
	Symbol    string // "FuncName", "TypeName.MethodName"
	Kind      string // "function", "method", "type", "interface", "const", "var", "package"
	StartLine int
	EndLine   int
	Content   string // raw source text, used for embedding
}

// Chunker splits source files into semantically meaningful chunks.
type Chunker interface {
	Supports(language string) bool
	Chunk(filePath string, content []byte) ([]Chunk, error)
}
```

Create `internal/chunker/goast.go`:

```go
package chunker

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// GoAST implements Chunker using Go's standard library AST parser.
type GoAST struct{}

// NewGoAST creates a new Go AST chunker.
func NewGoAST() *GoAST {
	return &GoAST{}
}

func (g *GoAST) Supports(language string) bool {
	return language == "go"
}

func (g *GoAST) Chunk(filePath string, content []byte) ([]Chunk, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}

	var chunks []Chunk

	// Package doc comment
	if file.Doc != nil {
		start := fset.Position(file.Doc.Pos())
		end := fset.Position(file.Package)
		// Include the "package <name>" line
		pkgEnd := fset.Position(file.Name.End())
		chunks = append(chunks, makeChunk(
			filePath,
			"package "+file.Name.Name,
			"package",
			start.Line,
			pkgEnd.Line,
			sliceContent(content, start.Offset, pkgEnd.Offset),
		))
	} else {
		// Even without a doc comment, create a package chunk
		start := fset.Position(file.Package)
		end := fset.Position(file.Name.End())
		chunks = append(chunks, makeChunk(
			filePath,
			"package "+file.Name.Name,
			"package",
			start.Line,
			end.Line,
			sliceContent(content, start.Offset, end.Offset),
		))
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			chunks = append(chunks, chunkFuncDecl(fset, filePath, content, d))
		case *ast.GenDecl:
			chunks = append(chunks, chunkGenDecl(fset, filePath, content, d)...)
		}
	}

	return chunks, nil
}

func chunkFuncDecl(fset *token.FileSet, filePath string, content []byte, d *ast.FuncDecl) Chunk {
	kind := "function"
	symbol := d.Name.Name

	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = "method"
		recvType := receiverTypeName(d.Recv.List[0].Type)
		symbol = recvType + "." + d.Name.Name
	}

	start, end := declRange(fset, d.Doc, d.Pos(), d.End())
	return makeChunk(filePath, symbol, kind, start.Line, end.Line,
		sliceContent(content, start.Offset, end.Offset))
}

func chunkGenDecl(fset *token.FileSet, filePath string, content []byte, d *ast.GenDecl) []Chunk {
	var chunks []Chunk

	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			kind := "type"
			if _, ok := s.Type.(*ast.InterfaceType); ok {
				kind = "interface"
			}
			// Use the GenDecl doc if the TypeSpec doesn't have its own
			doc := s.Doc
			if doc == nil {
				doc = d.Doc
			}
			start, end := declRange(fset, doc, d.Pos(), d.End())
			// For single-spec GenDecls, use the whole GenDecl range
			// For multi-spec, use just the spec
			if len(d.Specs) == 1 {
				start, end = declRange(fset, doc, d.Pos(), d.End())
			} else {
				start, end = declRange(fset, s.Doc, s.Pos(), s.End())
			}
			chunks = append(chunks, makeChunk(filePath, s.Name.Name, kind,
				start.Line, end.Line, sliceContent(content, start.Offset, end.Offset)))

		case *ast.ValueSpec:
			kind := "var"
			if d.Tok == token.CONST {
				kind = "const"
			}
			symbol := s.Names[0].Name
			doc := s.Doc
			if doc == nil {
				doc = d.Doc
			}
			start, end := declRange(fset, doc, d.Pos(), d.End())
			if len(d.Specs) == 1 {
				start, end = declRange(fset, doc, d.Pos(), d.End())
			} else {
				start, end = declRange(fset, s.Doc, s.Pos(), s.End())
			}
			chunks = append(chunks, makeChunk(filePath, symbol, kind,
				start.Line, end.Line, sliceContent(content, start.Offset, end.Offset)))
		}
	}

	return chunks
}

func declRange(fset *token.FileSet, doc *ast.CommentGroup, pos, end token.Pos) (token.Position, token.Position) {
	startPos := fset.Position(pos)
	if doc != nil {
		startPos = fset.Position(doc.Pos())
	}
	endPos := fset.Position(end)
	return startPos, endPos
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return "unknown"
}

func sliceContent(content []byte, startOffset, endOffset int) string {
	if startOffset < 0 {
		startOffset = 0
	}
	if endOffset > len(content) {
		endOffset = len(content)
	}
	return string(content[startOffset:endOffset])
}

func makeChunk(filePath, symbol, kind string, startLine, endLine int, content string) Chunk {
	raw := fmt.Sprintf("%s:%s:%d", filePath, symbol, startLine)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	return Chunk{
		ID:        hash[:16],
		FilePath:  filePath,
		Language:  "go",
		Symbol:    symbol,
		Kind:      kind,
		StartLine: startLine,
		EndLine:   endLine,
		Content:   content,
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/chunker/ -v` Expected: All 8 tests PASS.

**Step 5: Commit**

```bash
git add internal/chunker/
git commit -m "feat: add go/ast chunker for function/method/type/interface/const/var extraction"
```

---

### Task 4: SQLite + sqlite-vec Store

**Files:**

- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`

**Step 1: Write the failing test**

Create `internal/store/store_test.go`:

```go
package store

import (
	"testing"

	"github.com/ory/agent-index/internal/chunker"
)

func TestNewStore_CreatesSchema(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Verify tables exist by querying them
	var count int
	err = s.db.QueryRow("SELECT count(*) FROM files").Scan(&count)
	if err != nil {
		t.Fatalf("files table missing: %v", err)
	}
	err = s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&count)
	if err != nil {
		t.Fatalf("chunks table missing: %v", err)
	}
	err = s.db.QueryRow("SELECT count(*) FROM project_meta").Scan(&count)
	if err != nil {
		t.Fatalf("project_meta table missing: %v", err)
	}
}

func TestStore_SetGetMeta(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetMeta("test_key", "test_value"); err != nil {
		t.Fatal(err)
	}
	val, err := s.GetMeta("test_key")
	if err != nil {
		t.Fatal(err)
	}
	if val != "test_value" {
		t.Fatalf("expected test_value, got %s", val)
	}
}

func TestStore_UpsertAndSearchVectors(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert a file record
	if err := s.UpsertFile("main.go", "abc123"); err != nil {
		t.Fatal(err)
	}

	// Insert chunks with vectors
	chunks := []chunker.Chunk{
		{ID: "chunk1", FilePath: "main.go", Symbol: "Hello", Kind: "function", StartLine: 1, EndLine: 5},
		{ID: "chunk2", FilePath: "main.go", Symbol: "World", Kind: "function", StartLine: 6, EndLine: 10},
	}
	vectors := [][]float32{
		{0.1, 0.2, 0.3, 0.4},
		{0.9, 0.8, 0.7, 0.6},
	}

	if err := s.InsertChunks(chunks, vectors); err != nil {
		t.Fatal(err)
	}

	// Search for something closer to chunk1
	query := []float32{0.1, 0.2, 0.3, 0.4}
	results, err := s.Search(query, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// chunk1 should be closest (exact match)
	if results[0].Symbol != "Hello" {
		t.Fatalf("expected Hello as closest, got %s", results[0].Symbol)
	}
}

func TestStore_SearchWithKindFilter(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.UpsertFile("main.go", "abc123")

	chunks := []chunker.Chunk{
		{ID: "c1", FilePath: "main.go", Symbol: "Hello", Kind: "function", StartLine: 1, EndLine: 5},
		{ID: "c2", FilePath: "main.go", Symbol: "Server", Kind: "type", StartLine: 6, EndLine: 10},
	}
	vectors := [][]float32{
		{0.1, 0.2, 0.3, 0.4},
		{0.1, 0.2, 0.3, 0.4}, // same vector, but different kind
	}
	s.InsertChunks(chunks, vectors)

	results, err := s.Search([]float32{0.1, 0.2, 0.3, 0.4}, 10, "type")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with kind=type, got %d", len(results))
	}
	if results[0].Kind != "type" {
		t.Fatalf("expected kind=type, got %s", results[0].Kind)
	}
}

func TestStore_DeleteFileChunks(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.UpsertFile("main.go", "abc123")
	chunks := []chunker.Chunk{
		{ID: "c1", FilePath: "main.go", Symbol: "Hello", Kind: "function", StartLine: 1, EndLine: 5},
	}
	vectors := [][]float32{{0.1, 0.2, 0.3, 0.4}}
	s.InsertChunks(chunks, vectors)

	if err := s.DeleteFileChunks("main.go"); err != nil {
		t.Fatal(err)
	}

	results, err := s.Search([]float32{0.1, 0.2, 0.3, 0.4}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results after delete, got %d", len(results))
	}
}

func TestStore_GetFileHashes(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.UpsertFile("a.go", "hash_a")
	s.UpsertFile("b.go", "hash_b")

	hashes, err := s.GetFileHashes()
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 {
		t.Fatalf("expected 2 file hashes, got %d", len(hashes))
	}
	if hashes["a.go"] != "hash_a" {
		t.Fatalf("expected hash_a, got %s", hashes["a.go"])
	}
}

func TestStore_Stats(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.UpsertFile("main.go", "abc123")
	chunks := []chunker.Chunk{
		{ID: "c1", FilePath: "main.go", Symbol: "Hello", Kind: "function", StartLine: 1, EndLine: 5},
		{ID: "c2", FilePath: "main.go", Symbol: "World", Kind: "function", StartLine: 6, EndLine: 10},
	}
	vectors := [][]float32{
		{0.1, 0.2, 0.3, 0.4},
		{0.5, 0.6, 0.7, 0.8},
	}
	s.InsertChunks(chunks, vectors)

	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalFiles != 1 {
		t.Fatalf("expected 1 file, got %d", stats.TotalFiles)
	}
	if stats.TotalChunks != 2 {
		t.Fatalf("expected 2 chunks, got %d", stats.TotalChunks)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/store/ -v` Expected: FAIL — `New`,
`Store` type not defined.

**Step 3: Implement store package**

Replace `internal/store/store.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/ory/agent-index/internal/chunker"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto()
}

// SearchResult is a single vector search result.
type SearchResult struct {
	FilePath  string
	Symbol    string
	Kind      string
	StartLine int
	EndLine   int
	Score     float32
}

// Stats holds index statistics.
type Stats struct {
	TotalFiles  int
	TotalChunks int
}

// Store manages SQLite + sqlite-vec storage.
type Store struct {
	db         *sql.DB
	dimensions int
}

// New opens or creates a SQLite database with the required schema.
// dimensions is the embedding vector size (e.g. 1024).
func New(dsn string, dimensions int) (*Store, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL mode and foreign keys
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", pragma, err)
		}
	}

	s := &Store{db: db, dimensions: dimensions}
	if err := s.ensureSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) ensureSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS files (
			path         TEXT PRIMARY KEY,
			content_hash TEXT NOT NULL,
			indexed_at   INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS project_meta (
			key   TEXT PRIMARY KEY,
			value TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id         TEXT PRIMARY KEY,
			file_path  TEXT NOT NULL,
			symbol     TEXT NOT NULL,
			kind       TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line   INTEGER NOT NULL,
			FOREIGN KEY (file_path) REFERENCES files(path) ON DELETE CASCADE
		)`,
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_chunks USING vec0(
			id TEXT PRIMARY KEY,
			embedding float[%d] distance_metric=cosine
		)`, s.dimensions),
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w\nSQL: %s", err, stmt)
		}
	}
	return nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// SetMeta sets a key-value pair in project_meta.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO project_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	return err
}

// GetMeta gets a value from project_meta. Returns empty string if not found.
func (s *Store) GetMeta(key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM project_meta WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// UpsertFile inserts or updates a file record.
func (s *Store) UpsertFile(path, contentHash string) error {
	_, err := s.db.Exec(
		`INSERT INTO files (path, content_hash, indexed_at) VALUES (?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET content_hash=excluded.content_hash, indexed_at=excluded.indexed_at`,
		path, contentHash, time.Now().Unix(),
	)
	return err
}

// InsertChunks batch-inserts chunks and their embedding vectors.
// chunks and vectors must have the same length.
func (s *Store) InsertChunks(chunks []chunker.Chunk, vectors [][]float32) error {
	if len(chunks) != len(vectors) {
		return fmt.Errorf("chunks/vectors length mismatch: %d vs %d", len(chunks), len(vectors))
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	chunkStmt, err := tx.Prepare(
		`INSERT OR REPLACE INTO chunks (id, file_path, symbol, kind, start_line, end_line) VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer chunkStmt.Close()

	vecStmt, err := tx.Prepare(
		`INSERT OR REPLACE INTO vec_chunks (id, embedding) VALUES (?, ?)`,
	)
	if err != nil {
		return err
	}
	defer vecStmt.Close()

	for i, c := range chunks {
		if _, err := chunkStmt.Exec(c.ID, c.FilePath, c.Symbol, c.Kind, c.StartLine, c.EndLine); err != nil {
			return fmt.Errorf("insert chunk %s: %w", c.ID, err)
		}
		blob, err := sqlite_vec.SerializeFloat32(vectors[i])
		if err != nil {
			return fmt.Errorf("serialize vector %s: %w", c.ID, err)
		}
		if _, err := vecStmt.Exec(c.ID, blob); err != nil {
			return fmt.Errorf("insert vec %s: %w", c.ID, err)
		}
	}

	return tx.Commit()
}

// DeleteFileChunks removes all chunks and their vectors for a given file.
func (s *Store) DeleteFileChunks(filePath string) error {
	// Get chunk IDs first for vec_chunks cleanup
	rows, err := s.db.Query("SELECT id FROM chunks WHERE file_path = ?", filePath)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, id := range ids {
		if _, err := tx.Exec("DELETE FROM vec_chunks WHERE id = ?", id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DELETE FROM chunks WHERE file_path = ?", filePath); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM files WHERE path = ?", filePath); err != nil {
		return err
	}

	return tx.Commit()
}

// Search performs a KNN vector search and returns the closest matches.
func (s *Store) Search(queryVec []float32, limit int, kindFilter string) ([]SearchResult, error) {
	blob, err := sqlite_vec.SerializeFloat32(queryVec)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT c.file_path, c.symbol, c.kind, c.start_line, c.end_line, v.distance
		FROM vec_chunks v
		JOIN chunks c ON v.id = c.id
		WHERE v.embedding MATCH ?
		AND k = ?`
	args := []any{blob, limit * 3} // over-fetch for post-filtering

	if kindFilter != "" {
		query += ` AND c.kind = ?`
		args = append(args, kindFilter)
	}

	query += ` ORDER BY v.distance LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.FilePath, &r.Symbol, &r.Kind, &r.StartLine, &r.EndLine, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetFileHashes returns all stored file path → content hash mappings.
func (s *Store) GetFileHashes() (map[string]string, error) {
	rows, err := s.db.Query("SELECT path, content_hash FROM files")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hashes := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		hashes[path] = hash
	}
	return hashes, rows.Err()
}

// Stats returns index statistics.
func (s *Store) Stats() (*Stats, error) {
	var stats Stats
	if err := s.db.QueryRow("SELECT count(*) FROM files").Scan(&stats.TotalFiles); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&stats.TotalChunks); err != nil {
		return nil, err
	}
	return &stats, nil
}

// DeleteAll drops all data (for model change invalidation).
func (s *Store) DeleteAll() error {
	for _, table := range []string{"vec_chunks", "chunks", "files", "project_meta"} {
		if _, err := s.db.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test ./internal/store/ -v` Expected: All 7 tests PASS.

**Note:** The `kind` filter query with sqlite-vec JOIN may require adjustment.
The `k = ?` parameter controls KNN count inside vec0, and the kind filter
happens in the JOIN. If results are too few due to pre-filtering, we over-fetch
by 3x then limit. This may need tuning — the test will validate correctness.

**Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat: add sqlite + sqlite-vec store with vector search and batch inserts"
```

---

### Task 5: Ollama Embedder

**Files:**

- Modify: `internal/embedder/embedder.go`
- Create: `internal/embedder/ollama.go`
- Create: `internal/embedder/ollama_test.go`

**Step 1: Write the failing test**

Create `internal/embedder/ollama_test.go`:

```go
package embedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockOllamaResponse matches Ollama's /api/embed response format.
type mockOllamaResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Model      string      `json:"model"`
}

func TestOllamaEmbedder_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := mockOllamaResponse{
			Model: "nomic-embed-text",
			Embeddings: [][]float32{
				{0.1, 0.2, 0.3, 0.4},
				{0.5, 0.6, 0.7, 0.8},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	e, err := NewOllama("nomic-embed-text", 4, server.URL)
	if err != nil {
		t.Fatal(err)
	}

	vecs, err := e.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 4 {
		t.Fatalf("expected 4 dimensions, got %d", len(vecs[0]))
	}
}

func TestOllamaEmbedder_Dimensions(t *testing.T) {
	e, _ := NewOllama("nomic-embed-text", 1024, "http://localhost:11434")
	if e.Dimensions() != 1024 {
		t.Fatalf("expected 1024, got %d", e.Dimensions())
	}
}

func TestOllamaEmbedder_ModelName(t *testing.T) {
	e, _ := NewOllama("nomic-embed-text", 1024, "http://localhost:11434")
	if e.ModelName() != "nomic-embed-text" {
		t.Fatalf("expected nomic-embed-text, got %s", e.ModelName())
	}
}

func TestOllamaEmbedder_Batching(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		input := req["input"].([]any)

		embeddings := make([][]float32, len(input))
		for i := range input {
			embeddings[i] = []float32{0.1, 0.2, 0.3, 0.4}
		}
		resp := mockOllamaResponse{Model: "test", Embeddings: embeddings}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	e, _ := NewOllama("test", 4, server.URL)
	// 50 texts with batch size 32 should make 2 calls
	texts := make([]string, 50)
	for i := range texts {
		texts[i] = "text"
	}

	vecs, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 50 {
		t.Fatalf("expected 50 vectors, got %d", len(vecs))
	}
	if callCount != 2 {
		t.Fatalf("expected 2 batch calls, got %d", callCount)
	}
}

func TestOllamaEmbedder_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	e, _ := NewOllama("test", 4, server.URL)
	_, err := e.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/embedder/ -v` Expected: FAIL — `NewOllama` not defined.

**Step 3: Implement Ollama embedder**

Update `internal/embedder/embedder.go`:

```go
package embedder

import "context"

// Embedder converts text chunks into vector embeddings.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	ModelName() string
}
```

Create `internal/embedder/ollama.go`:

```go
package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBatchSize = 32

// Ollama implements Embedder using a local Ollama server.
type Ollama struct {
	model      string
	dimensions int
	baseURL    string
	client     *http.Client
}

// NewOllama creates an Ollama embedder.
// baseURL is the Ollama server URL (e.g. "http://localhost:11434").
func NewOllama(model string, dimensions int, baseURL string) (*Ollama, error) {
	return &Ollama{
		model:      model,
		dimensions: dimensions,
		baseURL:    baseURL,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}, nil
}

func (o *Ollama) Dimensions() int    { return o.dimensions }
func (o *Ollama) ModelName() string  { return o.model }

func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	var all [][]float32
	for i := 0; i < len(texts); i += defaultBatchSize {
		end := i + defaultBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		vecs, err := o.embedBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("embed batch %d-%d: %w", i, end, err)
		}
		all = append(all, vecs...)
	}
	return all, nil
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (o *Ollama) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: o.model, Input: texts})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, lastErr = o.client.Do(req)
		if lastErr == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			// Re-create request for retry (body was consumed)
			req, _ = http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/embed", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("ollama request failed after retries: %w", lastErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}
	return result.Embeddings, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/embedder/ -v` Expected: All 5 tests PASS.

**Step 5: Commit**

```bash
git add internal/embedder/
git commit -m "feat: add ollama embedder with batching and retry"
```

---

### Task 6: Index Orchestrator

**Files:**

- Create: `internal/index/index.go`
- Create: `internal/index/index_test.go`

**Step 1: Write the failing test**

Create `internal/index/index_test.go`:

```go
package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// mockEmbedder returns fixed vectors for testing.
type mockEmbedder struct {
	dims     int
	model    string
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

func (m *mockEmbedder) Dimensions() int   { return m.dims }
func (m *mockEmbedder) ModelName() string  { return m.model }

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

	// Index the project
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

	// Search
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

	// First index
	idx.Index(context.Background(), projectDir, false)
	firstCallCount := emb.callCount

	// Index again — no changes, should not re-embed
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

	// Modify the file
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

	// Force re-index — should re-embed even though nothing changed
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
```

**Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/index/ -v` Expected: FAIL — `NewIndexer`
not defined.

**Step 3: Implement index orchestrator**

Replace `internal/index/index.go`:

```go
package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ory/agent-index/internal/chunker"
	"github.com/ory/agent-index/internal/embedder"
	"github.com/ory/agent-index/internal/merkle"
	"github.com/ory/agent-index/internal/store"
)

// IndexStats is returned after an indexing operation.
type IndexStats struct {
	TotalFiles    int
	IndexedFiles  int
	ChunksCreated int
	FilesChanged  int
}

// StatusInfo is returned by the index_status tool.
type StatusInfo struct {
	ProjectPath    string
	TotalFiles     int
	IndexedFiles   int
	TotalChunks    int
	StaleFiles     int
	LastIndexedAt  string
	EmbeddingModel string
}

// Indexer orchestrates chunking, embedding, and storage.
type Indexer struct {
	store   *store.Store
	embedder embedder.Embedder
	chunker  chunker.Chunker
}

// NewIndexer creates an Indexer with the given SQLite DSN and embedder.
func NewIndexer(dsn string, emb embedder.Embedder) (*Indexer, error) {
	s, err := store.New(dsn, emb.Dimensions())
	if err != nil {
		return nil, err
	}
	return &Indexer{
		store:    s,
		embedder: emb,
		chunker:  chunker.NewGoAST(),
	}, nil
}

// Close closes the underlying store.
func (idx *Indexer) Close() error {
	return idx.store.Close()
}

// Index indexes or re-indexes a project directory.
func (idx *Indexer) Index(ctx context.Context, projectDir string, force bool) (*IndexStats, error) {
	// Check for model change
	storedModel, _ := idx.store.GetMeta("embedding_model")
	if storedModel != "" && storedModel != idx.embedder.ModelName() {
		// Model changed — wipe everything
		idx.store.DeleteAll()
		force = true
	}

	// Build current Merkle tree
	currentTree, err := merkle.BuildTree(projectDir, nil)
	if err != nil {
		return nil, fmt.Errorf("build merkle tree: %w", err)
	}

	stats := &IndexStats{TotalFiles: len(currentTree.Files)}

	if !force {
		storedHash, _ := idx.store.GetMeta("root_hash")
		if storedHash == currentTree.RootHash {
			// Nothing changed
			storeStats, _ := idx.store.Stats()
			if storeStats != nil {
				stats.IndexedFiles = storeStats.TotalFiles
			}
			return stats, nil
		}
	}

	// Determine which files changed
	var filesToIndex []string
	if force {
		for path := range currentTree.Files {
			filesToIndex = append(filesToIndex, path)
		}
	} else {
		// Build old tree from stored file hashes
		storedHashes, _ := idx.store.GetFileHashes()
		oldTree := &merkle.Tree{Files: storedHashes}
		added, _, modified := merkle.Diff(oldTree, currentTree)
		filesToIndex = append(added, modified...)

		// Handle removed files
		_, removed, _ := merkle.Diff(oldTree, currentTree)
		for _, path := range removed {
			idx.store.DeleteFileChunks(path)
		}
	}

	stats.FilesChanged = len(filesToIndex)
	if len(filesToIndex) == 0 {
		return stats, nil
	}

	// Chunk all changed files
	var allChunks []chunker.Chunk
	for _, relPath := range filesToIndex {
		absPath := filepath.Join(projectDir, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			continue // skip unreadable files
		}

		// Delete old chunks for this file
		idx.store.DeleteFileChunks(relPath)

		chunks, err := idx.chunker.Chunk(relPath, content)
		if err != nil {
			continue // skip unparseable files
		}

		// Update file hash
		idx.store.UpsertFile(relPath, currentTree.Files[relPath])
		allChunks = append(allChunks, chunks...)
	}

	if len(allChunks) == 0 {
		idx.store.SetMeta("root_hash", currentTree.RootHash)
		idx.store.SetMeta("embedding_model", idx.embedder.ModelName())
		idx.store.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))
		return stats, nil
	}

	// Embed all chunks
	texts := make([]string, len(allChunks))
	for i, c := range allChunks {
		texts[i] = c.Content
	}

	vectors, err := idx.embedder.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed chunks: %w", err)
	}

	// Store chunks + vectors
	if err := idx.store.InsertChunks(allChunks, vectors); err != nil {
		return nil, fmt.Errorf("insert chunks: %w", err)
	}

	// Update metadata
	idx.store.SetMeta("root_hash", currentTree.RootHash)
	idx.store.SetMeta("embedding_model", idx.embedder.ModelName())
	idx.store.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))

	stats.IndexedFiles = len(filesToIndex)
	stats.ChunksCreated = len(allChunks)
	return stats, nil
}

// Search performs a semantic search. Auto-indexes if the index is stale.
func (idx *Indexer) Search(ctx context.Context, projectDir string, queryVec []float32, limit int, kindFilter string) ([]store.SearchResult, error) {
	return idx.store.Search(queryVec, limit, kindFilter)
}

// EnsureFresh checks if the index is stale and re-indexes if needed.
func (idx *Indexer) EnsureFresh(ctx context.Context, projectDir string) (bool, *IndexStats, error) {
	storedHash, _ := idx.store.GetMeta("root_hash")
	currentTree, err := merkle.BuildTree(projectDir, nil)
	if err != nil {
		return false, nil, err
	}

	if storedHash == currentTree.RootHash {
		return false, nil, nil
	}

	stats, err := idx.Index(ctx, projectDir, false)
	return true, stats, err
}

// Status returns the current index status for a project.
func (idx *Indexer) Status(projectDir string) (*StatusInfo, error) {
	storeStats, err := idx.store.Stats()
	if err != nil {
		return nil, err
	}

	lastIndexed, _ := idx.store.GetMeta("last_indexed_at")
	model, _ := idx.store.GetMeta("embedding_model")

	// Count stale files
	storedHashes, _ := idx.store.GetFileHashes()
	currentTree, _ := merkle.BuildTree(projectDir, nil)
	added, removed, modified := merkle.Diff(
		&merkle.Tree{Files: storedHashes},
		currentTree,
	)

	return &StatusInfo{
		ProjectPath:    projectDir,
		TotalFiles:     len(currentTree.Files),
		IndexedFiles:   storeStats.TotalFiles,
		TotalChunks:    storeStats.TotalChunks,
		StaleFiles:     len(added) + len(removed) + len(modified),
		LastIndexedAt:  lastIndexed,
		EmbeddingModel: model,
	}, nil
}
```

**Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test ./internal/index/ -v` Expected: All 5 tests PASS.

**Step 5: Commit**

```bash
git add internal/index/
git commit -m "feat: add index orchestrator with merkle-based incremental indexing"
```

---

### Task 7: MCP Server Wiring

**Files:**

- Modify: `main.go`

**Step 1: Implement main.go with MCP server**

Replace `main.go`:

```go
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/ory/agent-index/internal/embedder"
	"github.com/ory/agent-index/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	model := envOrDefault("AGENT_INDEX_EMBED_MODEL", "nomic-embed-text")
	dims := 1024 // nomic-embed-text default
	ollamaHost := envOrDefault("OLLAMA_HOST", "http://localhost:11434")

	emb, err := embedder.NewOllama(model, dims, ollamaHost)
	if err != nil {
		log.Fatalf("create embedder: %v", err)
	}

	// Indexers are created per-project (lazily), but for simplicity
	// we manage them in the tool handlers via a cache.
	indexers := &indexerCache{embedder: emb}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "agent-index",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "semantic_search",
		Description: "Search indexed codebase using natural language. Returns file paths and line ranges of semantically matching code chunks. Auto-indexes if the index is stale or empty.",
	}, indexers.handleSemanticSearch)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "index_status",
		Description: "Check the indexing status of a project. Shows total files, indexed chunks, stale files, and embedding model.",
	}, indexers.handleIndexStatus)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// --- Tool input/output types ---

type SemanticSearchInput struct {
	Query        string `json:"query" jsonschema:"Natural language search query"`
	Path         string `json:"path" jsonschema:"Absolute path to the project root"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Max results to return, default 10"`
	Kind         string `json:"kind,omitempty" jsonschema:"Filter by chunk kind: function method type interface const var"`
	ForceReindex bool   `json:"force_reindex,omitempty" jsonschema:"Force full re-index before searching"`
}

type SearchResultItem struct {
	FilePath  string  `json:"file_path"`
	Symbol    string  `json:"symbol"`
	Kind      string  `json:"kind"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float32 `json:"score"`
}

type SemanticSearchOutput struct {
	Results      []SearchResultItem `json:"results"`
	Reindexed    bool               `json:"reindexed"`
	IndexedFiles int                `json:"indexed_files,omitempty"`
}

type IndexStatusInput struct {
	Path string `json:"path" jsonschema:"Absolute path to the project root"`
}

type IndexStatusOutput struct {
	ProjectPath    string `json:"project_path"`
	TotalFiles     int    `json:"total_files"`
	IndexedFiles   int    `json:"indexed_files"`
	TotalChunks    int    `json:"total_chunks"`
	StaleFiles     int    `json:"stale_files"`
	LastIndexedAt  string `json:"last_indexed_at"`
	EmbeddingModel string `json:"embedding_model"`
}

// --- Indexer cache (one indexer per project path) ---

type indexerCache struct {
	embedder embedder.Embedder
	cache    map[string]*index.Indexer
}

func (ic *indexerCache) getOrCreate(projectPath string) (*index.Indexer, error) {
	if ic.cache == nil {
		ic.cache = make(map[string]*index.Indexer)
	}
	if idx, ok := ic.cache[projectPath]; ok {
		return idx, nil
	}

	dbPath := dbPathForProject(projectPath)
	os.MkdirAll(filepath.Dir(dbPath), 0o755)

	idx, err := index.NewIndexer(dbPath, ic.embedder)
	if err != nil {
		return nil, err
	}
	ic.cache[projectPath] = idx
	return idx, nil
}

func (ic *indexerCache) handleSemanticSearch(ctx context.Context, req *mcp.CallToolRequest, input SemanticSearchInput) (*mcp.CallToolResult, SemanticSearchOutput, error) {
	if input.Path == "" {
		return nil, SemanticSearchOutput{}, fmt.Errorf("path is required")
	}
	if input.Query == "" {
		return nil, SemanticSearchOutput{}, fmt.Errorf("query is required")
	}
	if input.Limit <= 0 {
		input.Limit = 10
	}

	idx, err := ic.getOrCreate(input.Path)
	if err != nil {
		return nil, SemanticSearchOutput{}, fmt.Errorf("open index: %w", err)
	}

	var output SemanticSearchOutput

	// Auto-index or force reindex
	if input.ForceReindex {
		stats, err := idx.Index(ctx, input.Path, true)
		if err != nil {
			return nil, output, fmt.Errorf("index: %w", err)
		}
		output.Reindexed = true
		output.IndexedFiles = stats.IndexedFiles
	} else {
		reindexed, stats, err := idx.EnsureFresh(ctx, input.Path)
		if err != nil {
			return nil, output, fmt.Errorf("ensure fresh: %w", err)
		}
		output.Reindexed = reindexed
		if stats != nil {
			output.IndexedFiles = stats.IndexedFiles
		}
	}

	// Embed the query
	queryVecs, err := ic.embedder.Embed(ctx, []string{input.Query})
	if err != nil {
		return nil, output, fmt.Errorf("embed query: %w", err)
	}
	if len(queryVecs) == 0 {
		return nil, output, fmt.Errorf("no query embedding returned")
	}

	// Search
	results, err := idx.Search(ctx, input.Path, queryVecs[0], input.Limit, input.Kind)
	if err != nil {
		return nil, output, fmt.Errorf("search: %w", err)
	}

	for _, r := range results {
		output.Results = append(output.Results, SearchResultItem{
			FilePath:  r.FilePath,
			Symbol:    r.Symbol,
			Kind:      r.Kind,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Score:     r.Score,
		})
	}

	return nil, output, nil
}

func (ic *indexerCache) handleIndexStatus(ctx context.Context, req *mcp.CallToolRequest, input IndexStatusInput) (*mcp.CallToolResult, IndexStatusOutput, error) {
	if input.Path == "" {
		return nil, IndexStatusOutput{}, fmt.Errorf("path is required")
	}

	idx, err := ic.getOrCreate(input.Path)
	if err != nil {
		return nil, IndexStatusOutput{}, fmt.Errorf("open index: %w", err)
	}

	status, err := idx.Status(input.Path)
	if err != nil {
		return nil, IndexStatusOutput{}, err
	}

	return nil, IndexStatusOutput{
		ProjectPath:    status.ProjectPath,
		TotalFiles:     status.TotalFiles,
		IndexedFiles:   status.IndexedFiles,
		TotalChunks:    status.TotalChunks,
		StaleFiles:     status.StaleFiles,
		LastIndexedAt:  status.LastIndexedAt,
		EmbeddingModel: status.EmbeddingModel,
	}, nil
}

// --- Helpers ---

func dbPathForProject(projectPath string) string {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(projectPath)))
	dataDir := xdgDataDir()
	return filepath.Join(dataDir, "agent-index", hash[:16], "index.db")
}

func xdgDataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
```

**Step 2: Verify build**

Run: `CGO_ENABLED=1 go build -o agent-index .` Expected: Clean build, produces
`agent-index` binary.

**Step 3: Run all tests**

Run: `CGO_ENABLED=1 go test ./... -v` Expected: All tests across all packages
PASS.

**Step 4: Commit**

```bash
git add main.go
git commit -m "feat: wire MCP server with semantic_search and index_status tools"
```

---

### Task 8: Remove Unused Types File + Clean Up

**Files:**

- Delete: `internal/types.go` (if it was created — types now live in their
  respective packages)
- Verify: `go mod tidy`

**Step 1: Clean up**

```bash
rm -f internal/types.go
go mod tidy
```

**Step 2: Run all tests**

Run: `CGO_ENABLED=1 go test ./... -v` Expected: All tests PASS.

**Step 3: Verify binary runs**

Run: `./agent-index 2>&1 | head -1` (it will wait for MCP stdio, so just verify
it starts) Expected: No crash on startup. The binary will block waiting for MCP
client input over stdin.

**Step 4: Commit**

```bash
git add -A
git commit -m "chore: clean up unused types, tidy deps"
```

---

### Task 9: Integration Test

**Files:**

- Create: `integration_test.go`

**Step 1: Write integration test**

Create `integration_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ory/agent-index/internal/embedder"
	"github.com/ory/agent-index/internal/index"
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

	// Create a temp project with real Go files
	projectDir := t.TempDir()
	writeFile(t, projectDir, "main.go", `package main

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
	writeFile(t, projectDir, "handler.go", `package main

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

	// Index
	ctx := context.Background()
	stats, err := idx.Index(ctx, projectDir, false)
	if err != nil {
		t.Fatalf("index failed: %v", err)
	}
	t.Logf("Indexed %d files, %d chunks", stats.IndexedFiles, stats.ChunksCreated)

	// Search for "authentication"
	queryVecs, err := emb.Embed(ctx, []string{"authentication token validation"})
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}

	results, err := idx.Search(ctx, projectDir, queryVecs[0], 5, "")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	t.Logf("Search results for 'authentication token validation':")
	for _, r := range results {
		t.Logf("  %s:%d-%d %s %s (score: %.4f)", r.FilePath, r.StartLine, r.EndLine, r.Kind, r.Symbol, r.Score)
	}

	if len(results) == 0 {
		t.Fatal("expected at least 1 search result")
	}

	// HandleAuth should be among top results
	foundAuth := false
	for _, r := range results {
		if r.Symbol == "HandleAuth" {
			foundAuth = true
			break
		}
	}
	if !foundAuth {
		t.Log("WARNING: HandleAuth not in top results — embedding quality may vary")
	}

	// Test incremental: modify a file
	writeFile(t, projectDir, "main.go", `package main

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

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

**Step 2: Run unit tests (no integration)**

Run: `CGO_ENABLED=1 go test ./... -v` Expected: All unit tests PASS. Integration
test is skipped (no `integration` build tag).

**Step 3: Run integration test (if Ollama is available)**

Run: `CGO_ENABLED=1 go test -tags integration -v -run TestIntegration` Expected:
PASS if Ollama is running with `nomic-embed-text` pulled. FAIL with clear error
if Ollama is not running.

**Step 4: Commit**

```bash
git add integration_test.go
git commit -m "test: add integration test for full index + search pipeline"
```

---

### Task 10: Add .gitignore and README

**Files:**

- Create: `.gitignore`

**Step 1: Create .gitignore**

```
# Binary
agent-index
agent-index

# IDE
.idea/
.vscode/
*.swp

# OS
.DS_Store
```

**Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: add gitignore"
```

---

## Summary

| Task | What                         | Depends On |
| ---- | ---------------------------- | ---------- |
| 1    | Project scaffolding + deps   | —          |
| 2    | Merkle tree change detection | 1          |
| 3    | Go AST chunker               | 1          |
| 4    | SQLite + sqlite-vec store    | 1          |
| 5    | Ollama embedder              | 1          |
| 6    | Index orchestrator           | 2, 3, 4, 5 |
| 7    | MCP server wiring            | 6          |
| 8    | Clean up + verify            | 7          |
| 9    | Integration test             | 8          |
| 10   | .gitignore                   | 9          |

Tasks 2-5 are independent and can be parallelized. Task 6 requires all of 2-5.
Tasks 7-10 are sequential.
