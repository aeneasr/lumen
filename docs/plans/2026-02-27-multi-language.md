# Multi-Language Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to
> implement this plan task-by-task.

**Goal:** Extend agent-index to index and semantically search any supported
language, not just Go, using `smacker/go-tree-sitter` for all non-Go files while
keeping go/ast for `.go`.

**Architecture:** Add a `TreeSitterChunker` (smacker-based, per-language) and a
`MultiChunker` dispatcher (extension → Chunker). `DefaultLanguages()` pre-wires
all supported languages. Update `merkle.DefaultSkip` to cover all extensions.
Wire `MultiChunker` into `NewIndexer`. The `Chunker` interface, `Chunk` struct,
`Store`, and MCP tools are all unchanged.

**Tech Stack:** Go 1.26, `github.com/smacker/go-tree-sitter` (new), existing
CGo/sqlite-vec stack.

---

## Task 1: Add smacker/go-tree-sitter dependency

**Files:**

- Modify: `go.mod`, `go.sum`

**Step 1: Add the dependency**

```bash
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter@latest
```

**Step 2: Verify it builds**

```bash
CGO_ENABLED=1 go build ./...
```

Expected: no errors. The package has CGo so `CGO_ENABLED=1` is required (same as
sqlite-vec — already in the project's build flow).

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add smacker/go-tree-sitter dependency"
```

---

## Task 2: Create `TreeSitterChunker`

**Files:**

- Create: `internal/chunker/treesitter.go`
- Create: `internal/chunker/treesitter_test.go`

### Background

`TreeSitterChunker` holds:

- a `*sitter.Language` (the grammar, e.g. Python)
- a slice of `compiledRule` — each rule is a compiled tree-sitter query + a kind
  string ("function", "type", etc.)

`Chunk()` creates a fresh parser per call (avoids thread-safety issues), parses
the content, runs each rule's query, and collects matches. Each match has two
captures:

- `@decl` — the whole declaration node (gives start/end lines and content)
- `@name` — the identifier node (gives symbol name)

**Step 1: Write the failing test (Python — simplest language)**

Create `internal/chunker/treesitter_test.go`:

```go
package chunker_test

import (
	"testing"

	sitter_py "github.com/smacker/go-tree-sitter/python"

	"github.com/ory/agent-index/internal/chunker"
)

var samplePython = []byte(`def greet(name):
    """Say hello."""
    return f"Hello, {name}"

class Animal:
    def speak(self):
        pass
`)

func TestTreeSitterChunker_Python(t *testing.T) {
	def := chunker.LanguageDef{
		Language: sitter_py.GetLanguage(),
		Queries: []chunker.QueryDef{
			{Pattern: `(function_definition name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_definition name: (identifier) @name) @decl`, Kind: "type"},
		},
	}
	c, err := chunker.NewTreeSitterChunker(def)
	if err != nil {
		t.Fatalf("NewTreeSitterChunker: %v", err)
	}

	chunks, err := c.Chunk("sample.py", samplePython)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	// greet function
	greet, ok := bySymbol["greet"]
	if !ok {
		t.Fatalf("expected chunk 'greet', got symbols: %v", symbolNames(chunks))
	}
	if greet.Kind != "function" {
		t.Errorf("greet.Kind = %q, want %q", greet.Kind, "function")
	}
	if greet.StartLine != 1 {
		t.Errorf("greet.StartLine = %d, want 1", greet.StartLine)
	}
	if greet.FilePath != "sample.py" {
		t.Errorf("greet.FilePath = %q, want %q", greet.FilePath, "sample.py")
	}
	if greet.ID == "" {
		t.Error("greet.ID is empty")
	}

	// Animal class
	animal, ok := bySymbol["Animal"]
	if !ok {
		t.Fatalf("expected chunk 'Animal', got symbols: %v", symbolNames(chunks))
	}
	if animal.Kind != "type" {
		t.Errorf("animal.Kind = %q, want %q", animal.Kind, "type")
	}
	if animal.StartLine != 5 {
		t.Errorf("animal.StartLine = %d, want 5", animal.StartLine)
	}

	// speak is a function_definition inside class — also matched by the function query
	speak, ok := bySymbol["speak"]
	if !ok {
		t.Fatalf("expected chunk 'speak', got symbols: %v", symbolNames(chunks))
	}
	if speak.Kind != "function" {
		t.Errorf("speak.Kind = %q, want %q", speak.Kind, "function")
	}
}

func symbolNames(chunks []chunker.Chunk) []string {
	names := make([]string, len(chunks))
	for i, c := range chunks {
		names[i] = c.Symbol
	}
	return names
}
```

**Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=1 go test ./internal/chunker/ -run TestTreeSitterChunker_Python -v
```

Expected: compile error — `chunker.LanguageDef`, `chunker.QueryDef`,
`chunker.NewTreeSitterChunker` do not exist yet.

**Step 3: Implement `internal/chunker/treesitter.go`**

```go
package chunker

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
)

// QueryDef defines a tree-sitter query pattern and the chunk kind it produces.
// Pattern must include captures @decl (full declaration node) and @name (identifier).
type QueryDef struct {
	Pattern string
	Kind    string
}

// LanguageDef bundles a tree-sitter language with its query patterns.
type LanguageDef struct {
	Language *sitter.Language
	Queries  []QueryDef
}

type compiledRule struct {
	query        *sitter.Query
	captureNames []string
	kind         string
}

// TreeSitterChunker implements Chunker using tree-sitter.
type TreeSitterChunker struct {
	language *sitter.Language
	rules    []compiledRule
}

// NewTreeSitterChunker compiles the queries in def and returns a TreeSitterChunker.
func NewTreeSitterChunker(def LanguageDef) (*TreeSitterChunker, error) {
	rules := make([]compiledRule, 0, len(def.Queries))
	for _, qd := range def.Queries {
		q, err := sitter.NewQuery([]byte(qd.Pattern), def.Language)
		if err != nil {
			return nil, fmt.Errorf("compile query for kind %q: %w", qd.Kind, err)
		}
		rules = append(rules, compiledRule{
			query:        q,
			captureNames: q.CaptureNames(),
			kind:         qd.Kind,
		})
	}
	return &TreeSitterChunker{language: def.Language, rules: rules}, nil
}

// mustTreeSitterChunker panics if NewTreeSitterChunker returns an error.
// Only for hardcoded, compile-time-known query patterns.
func mustTreeSitterChunker(def LanguageDef) *TreeSitterChunker {
	c, err := NewTreeSitterChunker(def)
	if err != nil {
		panic(fmt.Sprintf("invalid tree-sitter query: %v", err))
	}
	return c
}

// Chunk parses content and returns semantic code chunks.
func (c *TreeSitterChunker) Chunk(filePath string, content []byte) ([]Chunk, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(c.language)
	tree := parser.Parse(nil, content)
	root := tree.RootNode()

	var chunks []Chunk

	for _, rule := range c.rules {
		qc := sitter.NewQueryCursor()
		qc.Exec(rule.query, root)

		for {
			m, ok := qc.NextMatch()
			if !ok {
				break
			}

			var declNode, nameNode *sitter.Node
			for _, cap := range m.Captures {
				if int(cap.Index) >= len(rule.captureNames) {
					continue
				}
				switch rule.captureNames[cap.Index] {
				case "decl":
					declNode = cap.Node
				case "name":
					nameNode = cap.Node
				}
			}

			if declNode == nil || nameNode == nil {
				continue
			}

			symbol := nameNode.Content(content)
			startLine := int(declNode.StartPoint().Row) + 1
			endLine := int(declNode.EndPoint().Row) + 1
			snippet := declNode.Content(content)

			chunks = append(chunks, makeChunk(filePath, symbol, rule.kind, startLine, endLine, snippet))
		}
	}

	return chunks, nil
}
```

**Step 4: Run test to verify it passes**

```bash
CGO_ENABLED=1 go test ./internal/chunker/ -run TestTreeSitterChunker_Python -v
```

Expected: PASS.

**Step 5: Add TypeScript and Rust tests to the same file**

Append to `internal/chunker/treesitter_test.go`:

```go
var sampleTypeScript = []byte(`export function add(a: number, b: number): number {
  return a + b;
}

export class Calculator {
  multiply(x: number, y: number): number {
    return x * y;
  }
}

export interface Shape {
  area(): number;
}

export type Color = "red" | "green" | "blue";
`)

func TestTreeSitterChunker_TypeScript(t *testing.T) {
	// Import at top of file: sitter_ts "github.com/smacker/go-tree-sitter/typescript/typescript"
	def := chunker.LanguageDef{
		Language: sitter_ts.GetLanguage(),
		Queries: []chunker.QueryDef{
			{Pattern: `(function_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(interface_declaration name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(type_alias_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(method_definition name: (property_identifier) @name) @decl`, Kind: "method"},
		},
	}
	c, err := chunker.NewTreeSitterChunker(def)
	if err != nil {
		t.Fatalf("NewTreeSitterChunker: %v", err)
	}

	chunks, err := c.Chunk("sample.ts", sampleTypeScript)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("add", "function")
	check("Calculator", "type")
	check("multiply", "method")
	check("Shape", "interface")
	check("Color", "type")
}

var sampleRust = []byte(`pub fn add(a: i32, b: i32) -> i32 {
    a + b
}

pub struct Point {
    pub x: f64,
    pub y: f64,
}

pub trait Drawable {
    fn draw(&self);
}

pub const MAX_SIZE: usize = 1024;
`)

func TestTreeSitterChunker_Rust(t *testing.T) {
	// Import at top of file: sitter_rs "github.com/smacker/go-tree-sitter/rust"
	def := chunker.LanguageDef{
		Language: sitter_rs.GetLanguage(),
		Queries: []chunker.QueryDef{
			{Pattern: `(function_item name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(struct_item name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(enum_item name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(trait_item name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(const_item name: (identifier) @name) @decl`, Kind: "const"},
		},
	}
	c, err := chunker.NewTreeSitterChunker(def)
	if err != nil {
		t.Fatalf("NewTreeSitterChunker: %v", err)
	}

	chunks, err := c.Chunk("sample.rs", sampleRust)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("add", "function")
	check("Point", "type")
	check("Drawable", "interface")
	check("MAX_SIZE", "const")
}
```

Update the import block at the top of treesitter_test.go to include:

```go
import (
	"testing"

	sitter_py "github.com/smacker/go-tree-sitter/python"
	sitter_rs "github.com/smacker/go-tree-sitter/rust"
	sitter_ts "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/ory/agent-index/internal/chunker"
)
```

**Step 6: Run all chunker tests**

```bash
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/python
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/rust
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/typescript/typescript
CGO_ENABLED=1 go test ./internal/chunker/ -run TestTreeSitterChunker -v
```

Expected: all three tests PASS.

**Step 7: Commit**

```bash
git add internal/chunker/treesitter.go internal/chunker/treesitter_test.go go.mod go.sum
git commit -m "feat: add TreeSitterChunker for multi-language AST parsing"
```

---

## Task 3: Create `MultiChunker` and `DefaultLanguages()`

**Files:**

- Create: `internal/chunker/multi.go`
- Create: `internal/chunker/languages.go`
- Modify: `internal/chunker/treesitter_test.go` (add MultiChunker tests)

### Part A: MultiChunker

**Step 1: Write the failing test for MultiChunker**

Append to `internal/chunker/treesitter_test.go`:

```go
func TestMultiChunker_Dispatch(t *testing.T) {
	goDef := chunker.LanguageDef{
		Language: sitter_py.GetLanguage(), // reuse python as a stand-in for test isolation
		Queries: []chunker.QueryDef{
			{Pattern: `(function_definition name: (identifier) @name) @decl`, Kind: "function"},
		},
	}
	pyChunker := chunker.MustTreeSitterChunker(goDef)

	mc := chunker.NewMultiChunker(map[string]chunker.Chunker{
		".py": pyChunker,
	})

	// Known extension — returns chunks
	chunks, err := mc.Chunk("foo.py", samplePython)
	if err != nil {
		t.Fatalf("Chunk(.py): %v", err)
	}
	if len(chunks) == 0 {
		t.Error("expected chunks for .py file, got none")
	}

	// Unknown extension — returns nil, nil (no error)
	chunks, err = mc.Chunk("foo.xyz", []byte("hello"))
	if err != nil {
		t.Fatalf("Chunk(.xyz): unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected no chunks for .xyz, got %d", len(chunks))
	}
}
```

Also export `MustTreeSitterChunker` (uppercase) from `treesitter.go` — rename
`mustTreeSitterChunker` to `MustTreeSitterChunker` for use in tests and
languages.go.

**Step 2: Run to verify it fails**

```bash
CGO_ENABLED=1 go test ./internal/chunker/ -run TestMultiChunker -v
```

Expected: compile error — `chunker.NewMultiChunker` does not exist.

**Step 3: Create `internal/chunker/multi.go`**

```go
package chunker

import "path/filepath"

// MultiChunker dispatches to per-extension Chunkers.
// Files with unrecognized extensions return (nil, nil).
type MultiChunker struct {
	chunkers map[string]Chunker
}

// NewMultiChunker creates a MultiChunker from a map of extension → Chunker.
// Extensions must include the leading dot, e.g. ".go", ".py".
func NewMultiChunker(chunkers map[string]Chunker) *MultiChunker {
	return &MultiChunker{chunkers: chunkers}
}

// Chunk dispatches to the appropriate Chunker based on the file extension.
// Returns nil, nil for unsupported extensions.
func (m *MultiChunker) Chunk(filePath string, content []byte) ([]Chunk, error) {
	ext := filepath.Ext(filePath)
	c, ok := m.chunkers[ext]
	if !ok {
		return nil, nil
	}
	return c.Chunk(filePath, content)
}
```

**Step 4: Run test to verify it passes**

```bash
CGO_ENABLED=1 go test ./internal/chunker/ -run TestMultiChunker -v
```

Expected: PASS.

### Part B: DefaultLanguages()

**Step 5: Fetch remaining language grammars**

```bash
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/javascript
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/typescript/tsx
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/ruby
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/java
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/c
CGO_ENABLED=1 go get github.com/smacker/go-tree-sitter/cpp
```

**Step 6: Create `internal/chunker/languages.go`**

```go
package chunker

import (
	sitter_c   "github.com/smacker/go-tree-sitter/c"
	sitter_cpp "github.com/smacker/go-tree-sitter/cpp"
	sitter_java "github.com/smacker/go-tree-sitter/java"
	sitter_js  "github.com/smacker/go-tree-sitter/javascript"
	sitter_py  "github.com/smacker/go-tree-sitter/python"
	sitter_rb  "github.com/smacker/go-tree-sitter/ruby"
	sitter_rs  "github.com/smacker/go-tree-sitter/rust"
	sitter_ts  "github.com/smacker/go-tree-sitter/typescript/typescript"
	sitter_tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
)

// supportedExtensions is the canonical list of file extensions the default
// language set handles. Kept in sync with DefaultLanguages() map keys.
var supportedExtensions = []string{
	".go",
	".ts", ".tsx",
	".js", ".jsx", ".mjs",
	".py",
	".rs",
	".rb",
	".java",
	".c", ".h",
	".cpp", ".cc", ".cxx", ".hpp",
}

// SupportedExtensions returns the file extensions indexed by DefaultLanguages.
func SupportedExtensions() []string { return supportedExtensions }

// DefaultLanguages returns a map of file extension → Chunker for all supported languages.
// It panics if any hardcoded query pattern is invalid (programming error).
func DefaultLanguages() map[string]Chunker {
	py := mustTreeSitterChunker(LanguageDef{
		Language: sitter_py.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_definition name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_definition name: (identifier) @name) @decl`, Kind: "type"},
		},
	})

	ts := mustTreeSitterChunker(LanguageDef{
		Language: sitter_ts.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(interface_declaration name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(type_alias_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(method_definition name: (property_identifier) @name) @decl`, Kind: "method"},
		},
	})

	tsx := mustTreeSitterChunker(LanguageDef{
		Language: sitter_tsx.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(interface_declaration name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(type_alias_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(method_definition name: (property_identifier) @name) @decl`, Kind: "method"},
		},
	})

	js := mustTreeSitterChunker(LanguageDef{
		Language: sitter_js.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_declaration name: (identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(method_definition name: (property_identifier) @name) @decl`, Kind: "method"},
			{Pattern: `(generator_function_declaration name: (identifier) @name) @decl`, Kind: "function"},
		},
	})

	rs := mustTreeSitterChunker(LanguageDef{
		Language: sitter_rs.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_item name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(struct_item name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(enum_item name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(trait_item name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(impl_item type: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(const_item name: (identifier) @name) @decl`, Kind: "const"},
		},
	})

	rb := mustTreeSitterChunker(LanguageDef{
		Language: sitter_rb.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(method name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(singleton_method name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class name: (constant) @name) @decl`, Kind: "type"},
			{Pattern: `(module name: (constant) @name) @decl`, Kind: "type"},
		},
	})

	java := mustTreeSitterChunker(LanguageDef{
		Language: sitter_java.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(method_declaration name: (identifier) @name) @decl`, Kind: "method"},
			{Pattern: `(class_declaration name: (identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(interface_declaration name: (identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(constructor_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(enum_declaration name: (identifier) @name) @decl`, Kind: "type"},
		},
	})

	c := mustTreeSitterChunker(LanguageDef{
		Language: sitter_c.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_definition declarator: (function_declarator declarator: (identifier) @name)) @decl`, Kind: "function"},
			{Pattern: `(struct_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(enum_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
		},
	})

	cpp := mustTreeSitterChunker(LanguageDef{
		Language: sitter_cpp.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_definition declarator: (function_declarator declarator: (identifier) @name)) @decl`, Kind: "function"},
			{Pattern: `(class_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(struct_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(enum_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
		},
	})

	goChunker := NewGoAST()

	return map[string]Chunker{
		".go":  goChunker,
		".ts":  ts,
		".tsx": tsx,
		".js":  js,
		".jsx": js,
		".mjs": js,
		".py":  py,
		".rs":  rs,
		".rb":  rb,
		".java": java,
		".c":   c,
		".h":   c,
		".cpp": cpp,
		".cc":  cpp,
		".cxx": cpp,
		".hpp": cpp,
	}
}
```

**Step 7: Write a smoke test for DefaultLanguages**

Append to `internal/chunker/treesitter_test.go`:

```go
func TestDefaultLanguages_AllExtensionsPresent(t *testing.T) {
	langs := chunker.DefaultLanguages()
	for _, ext := range chunker.SupportedExtensions() {
		if _, ok := langs[ext]; !ok {
			t.Errorf("DefaultLanguages() missing extension %q", ext)
		}
	}
}
```

**Step 8: Run all chunker tests**

```bash
CGO_ENABLED=1 go test ./internal/chunker/ -v
```

Expected: all tests PASS.

**Step 9: Commit**

```bash
git add internal/chunker/multi.go internal/chunker/languages.go internal/chunker/treesitter_test.go go.mod go.sum
git commit -m "feat: add MultiChunker and DefaultLanguages for all supported file types"
```

---

## Task 4: Update Merkle skip and wire MultiChunker into Indexer

**Files:**

- Modify: `internal/merkle/merkle.go`
- Modify: `internal/index/index.go`

**Step 1: Add `MakeExtSkip` to merkle**

In `internal/merkle/merkle.go`, add after the `DefaultSkip` function:

```go
// MakeExtSkip returns a SkipFunc that passes only files whose extension is in exts.
// The standard skip directories (.git, vendor, testdata, node_modules, _build) are always applied.
func MakeExtSkip(exts []string) SkipFunc {
	extSet := make(map[string]bool, len(exts))
	for _, ext := range exts {
		extSet[ext] = true
	}
	return func(relPath string, isDir bool) bool {
		base := filepath.Base(relPath)
		if isDir {
			switch base {
			case ".git", "vendor", "testdata", "node_modules", "_build":
				return true
			}
			return false
		}
		return !extSet[filepath.Ext(relPath)]
	}
}
```

**Step 2: Update `internal/index/index.go`**

Change the `NewIndexer` function to use `MultiChunker`:

```go
// Add to imports:
//   "github.com/ory/agent-index/internal/merkle"

func NewIndexer(dsn string, emb embedder.Embedder) (*Indexer, error) {
	s, err := store.New(dsn, emb.Dimensions())
	if err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}
	return &Indexer{
		store:   s,
		emb:     emb,
		chunker: chunker.NewMultiChunker(chunker.DefaultLanguages()),
	}, nil
}
```

Also update both `BuildTree` calls in `index.go` (there are two — in `Index` and
`EnsureFresh`) to use `MakeExtSkip`:

```go
// Change:
curTree, err := merkle.BuildTree(projectDir, nil)
// To:
curTree, err := merkle.BuildTree(projectDir, merkle.MakeExtSkip(chunker.SupportedExtensions()))
```

**Important:** There are two `merkle.BuildTree` calls — one in `Index()` and one
in `EnsureFresh()`. Update both.

**Step 3: Run the full test suite**

```bash
CGO_ENABLED=1 go test ./... -v
```

Expected: all tests PASS. The existing go/ast tests, store tests, and merkle
tests should all continue to pass. The indexer tests should pass since `.go`
files are still handled by `GoAST` via the MultiChunker.

**Step 4: Commit**

```bash
git add internal/merkle/merkle.go internal/index/index.go
git commit -m "feat: wire MultiChunker and MakeExtSkip for multi-language indexing"
```

---

## Task 5: Verify tests with subagent review

After all tests pass:

1. Launch a `pr-review-toolkit:pr-test-analyzer` subagent to verify that test
   cases are meaningful and match the implementation.

2. Address any issues the subagent identifies.

3. Run final check:

```bash
CGO_ENABLED=1 go test ./... -count=1
```

Expected: all PASS, no flakes.

**Final commit and push:**

```bash
git add -A
git commit -m "feat: multi-language semantic code indexing via tree-sitter"
git push origin initial-implementation
```
