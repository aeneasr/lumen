# Structured YAML/JSON Chunker Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to
> implement this plan task-by-task.

**Goal:** Replace the plain-text `DataChunker` for YAML/JSON with a recursive,
structure-aware `StructuredChunker` that splits at YAML/JSON key boundaries
instead of line boundaries.

**Architecture:** Parse YAML/JSON into `yaml.v3` nodes (yaml.v3 understands both
formats). Walk the tree depth-first: if a subtree fits within the token budget,
emit it as one chunk with its dotted key path prepended to the content. If it's
too large, recurse into its children. `splitOversizedChunks` in `index/split.go`
remains the backstop for leaf nodes that cannot be subdivided further. Small
files (whole file ≤ maxChars) pass through as a single `document` chunk,
preserving the current fast path.

**Tech Stack:** `gopkg.in/yaml.v3` (adds position-aware YAML/JSON parsing), Go
stdlib only otherwise.

---

## Context / Key Files

- `internal/chunker/chunker.go` — `Chunk` struct and `Chunker` interface
- `internal/chunker/goast.go` — `makeChunk()` helper (shared by all chunkers)
- `internal/chunker/data.go` — current DataChunker (plain-text, to be
  superseded)
- `internal/chunker/data_test.go` — DataChunker unit tests (keep, still test
  DataChunker in isolation)
- `internal/chunker/languages.go` — `DefaultLanguages()` wires
  `.yaml`/`.yml`/`.json` → DataChunker (change to StructuredChunker)
- `internal/chunker/markdown.go` — good reference: heading-based structural
  chunker, same pattern we're implementing for YAML/JSON
- `internal/index/split.go` — `splitOversizedChunks` backstop (do not change)
- `internal/config/config.go` —
  `EnvOrDefaultInt("AGENT_INDEX_MAX_CHUNK_TOKENS", 2048)`
- `testdata/snapshots/TestLang_YAML-*` — 4 snapshots, **already deleted**, will
  be recreated
- `testdata/snapshots/TestLang_JSON-*` — 4 snapshots, **already deleted**, will
  be recreated

---

## Task 1: Add `gopkg.in/yaml.v3` dependency

**Files:**

- Modify: `go.mod`, `go.sum`

**Step 1: Add the dependency**

```bash
cd /Users/aeneas/workspace/go/agent-index-go
go get gopkg.in/yaml.v3
```

Expected: `go.mod` gains `gopkg.in/yaml.v3 vX.X.X`.

**Step 2: Verify build**

```bash
go build ./...
```

Expected: no errors.

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add gopkg.in/yaml.v3 for structured YAML/JSON chunker"
```

---

## Task 2: Implement `StructuredChunker`

**Files:**

- Create: `internal/chunker/structured.go`

**Step 1: Write the failing test first** (see Task 3 — do Task 3 Step 1 before
implementing)

**Step 2: Implement `internal/chunker/structured.go`**

```go
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

package chunker

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// StructuredChunker splits YAML and JSON files by their key hierarchy.
// It recursively descends the document tree: if a subtree fits within the
// token budget, it is emitted as one chunk with its dotted key path prepended.
// If a subtree is too large, the chunker recurses into its children.
// The splitOversizedChunks pipeline in index/split.go is the backstop for
// leaf nodes that cannot be subdivided further.
type StructuredChunker struct {
	maxChars int // maxTokens * 4 (1 token ≈ 4 chars)
}

// NewStructuredChunker returns a StructuredChunker. maxTokens is the token
// budget per chunk; use AGENT_INDEX_MAX_CHUNK_TOKENS (default 2048).
func NewStructuredChunker(maxTokens int) *StructuredChunker {
	return &StructuredChunker{maxChars: maxTokens * 4}
}

// Chunk implements Chunker for YAML and JSON files.
func (c *StructuredChunker) Chunk(filePath string, content []byte) ([]Chunk, error) {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return nil, nil
	}

	// Fast path: small file fits as a single chunk.
	if len(trimmed) <= c.maxChars {
		lines := strings.Count(trimmed, "\n") + 1
		return []Chunk{makeChunk(filePath, "root", "document", 1, lines, trimmed)}, nil
	}

	// Parse into a yaml.Node tree. yaml.v3 understands both YAML and JSON.
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var chunks []Chunk
	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			break // EOF or parse error: return what we have
		}
		if doc.Kind == 0 || len(doc.Content) == 0 {
			continue
		}
		// doc is always a DocumentNode; doc.Content[0] is the root.
		root := doc.Content[0]
		chunks = append(chunks, c.recurse(filePath, root, "")...)
	}

	// If parsing produced nothing (e.g. parse error on large file), fall back.
	if len(chunks) == 0 {
		lines := strings.Count(trimmed, "\n") + 1
		return []Chunk{makeChunk(filePath, "root", "document", 1, lines, trimmed)}, nil
	}
	return chunks, nil
}

// recurse emits chunks for the given node. If the node serializes within
// maxChars, it emits a single chunk. Otherwise it recurses into children.
func (c *StructuredChunker) recurse(filePath string, node *yaml.Node, path string) []Chunk {
	text := serialize(node)
	symbol := path
	if symbol == "" {
		symbol = "root"
	}

	if len(text) <= c.maxChars {
		content := "# path: " + symbol + "\n" + text
		startLine := node.Line
		if startLine == 0 {
			startLine = 1
		}
		endLine := startLine + strings.Count(text, "\n")
		return []Chunk{makeChunk(filePath, symbol, "section", startLine, endLine, content)}
	}

	switch node.Kind {
	case yaml.MappingNode:
		// Content alternates: key₀, val₀, key₁, val₁, ...
		var chunks []Chunk
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			childPath := joinPath(path, keyNode.Value)
			// Emit key+value as a wrapper so the chunk shows "key: value" not just the value.
			wrapper := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{keyNode, valNode}}
			wrapText := serialize(wrapper)
			childSymbol := childPath
			if len(wrapText) <= c.maxChars {
				content := "# path: " + childSymbol + "\n" + wrapText
				startLine := keyNode.Line
				if startLine == 0 {
					startLine = 1
				}
				endLine := startLine + strings.Count(wrapText, "\n")
				chunks = append(chunks, makeChunk(filePath, childSymbol, "section", startLine, endLine, content))
			} else {
				// Value itself is too large — recurse into it.
				chunks = append(chunks, c.recurse(filePath, valNode, childPath)...)
			}
		}
		return chunks

	case yaml.SequenceNode:
		var chunks []Chunk
		for i, item := range node.Content {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			chunks = append(chunks, c.recurse(filePath, item, childPath)...)
		}
		return chunks

	default:
		// Scalar or unknown: emit as-is; splitOversizedChunks handles if huge.
		content := "# path: " + symbol + "\n" + text
		startLine := node.Line
		if startLine == 0 {
			startLine = 1
		}
		endLine := startLine + strings.Count(text, "\n")
		return []Chunk{makeChunk(filePath, symbol, "section", startLine, endLine, content)}
	}
}

// serialize marshals a yaml.Node to text. Returns empty string on error.
func serialize(node *yaml.Node) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}

// joinPath builds "parent.child"; if parent is empty returns child.
func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}
```

**Step 3: Build**

```bash
go build ./internal/chunker/...
```

Expected: no errors.

---

## Task 3: Unit tests for `StructuredChunker`

**Files:**

- Create: `internal/chunker/structured_test.go`

**Step 1: Write the tests**

```go
// Copyright 2026 Aeneas Rekkas
//
// Licensed under the Apache License, Version 2.0 (the "License")...

package chunker

import (
	"strings"
	"testing"
)

func TestStructuredChunker_SmallYAML_SingleChunk(t *testing.T) {
	// Small file: must pass through as a single "document" chunk (fast path).
	input := []byte("name: foo\nversion: 1\n")
	c := NewStructuredChunker(2048)
	chunks, err := c.Chunk("test.yaml", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Kind != "document" {
		t.Errorf("Kind = %q, want document", chunks[0].Kind)
	}
	if chunks[0].Symbol != "root" {
		t.Errorf("Symbol = %q, want root", chunks[0].Symbol)
	}
}

func TestStructuredChunker_LargeYAML_SplitsAtTopLevelKeys(t *testing.T) {
	// Build a YAML with two top-level keys, each just over budget.
	// maxTokens=1 → maxChars=4, so any non-trivial content triggers recursion.
	// Use maxTokens=2 (8 chars) to force splitting.
	var sb strings.Builder
	// Two top-level keys, each with enough content to exceed the small budget.
	sb.WriteString("alpha:\n")
	for i := 0; i < 10; i++ {
		sb.WriteString("  key" + string(rune('a'+i)) + ": value\n")
	}
	sb.WriteString("beta:\n")
	for i := 0; i < 10; i++ {
		sb.WriteString("  key" + string(rune('a'+i)) + ": value\n")
	}

	c := NewStructuredChunker(2) // 2 tokens = 8 chars, forces splitting
	chunks, err := c.Chunk("test.yaml", []byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}

	// Must have at least 2 chunks (one for alpha, one for beta).
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", len(chunks))
	}

	// All chunks must have kind "section".
	for _, ch := range chunks {
		if ch.Kind != "section" {
			t.Errorf("chunk %q: Kind = %q, want section", ch.Symbol, ch.Kind)
		}
	}

	// All chunks must have a "# path:" prefix in Content.
	for _, ch := range chunks {
		if !strings.HasPrefix(ch.Content, "# path:") {
			t.Errorf("chunk %q: Content missing path prefix: %q", ch.Symbol, ch.Content[:min(40, len(ch.Content))])
		}
	}

	// Symbols must contain "alpha" or "beta".
	symbols := make(map[string]bool)
	for _, ch := range chunks {
		symbols[ch.Symbol] = true
	}
	if !symbols["alpha"] && !containsPrefix(symbols, "alpha.") {
		t.Errorf("no chunk for top-level key 'alpha'; symbols: %v", symbolKeys(symbols))
	}
	if !symbols["beta"] && !containsPrefix(symbols, "beta.") {
		t.Errorf("no chunk for top-level key 'beta'; symbols: %v", symbolKeys(symbols))
	}
}

func TestStructuredChunker_JSON_SmallFile(t *testing.T) {
	input := []byte(`{"name":"foo","version":"1"}`)
	c := NewStructuredChunker(2048)
	chunks, err := c.Chunk("test.json", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Kind != "document" {
		t.Errorf("Kind = %q, want document", chunks[0].Kind)
	}
}

func TestStructuredChunker_JSON_LargeFile_SplitsAtKeys(t *testing.T) {
	// Large JSON with two top-level keys.
	var sb strings.Builder
	sb.WriteString(`{"dependencies":{`)
	for i := 0; i < 20; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"pkg` + string(rune('a'+i)) + `":"1.0.0"`)
	}
	sb.WriteString(`},"devDependencies":{`)
	for i := 0; i < 20; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"dev` + string(rune('a'+i)) + `":"2.0.0"`)
	}
	sb.WriteString(`}}`)

	c := NewStructuredChunker(2) // tiny budget forces splitting
	chunks, err := c.Chunk("test.json", []byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", len(chunks))
	}
	for _, ch := range chunks {
		if !strings.HasPrefix(ch.Content, "# path:") {
			t.Errorf("chunk %q: missing path prefix", ch.Symbol)
		}
	}
}

func TestStructuredChunker_Empty(t *testing.T) {
	c := NewStructuredChunker(2048)
	chunks, err := c.Chunk("test.yaml", []byte("   "))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestStructuredChunker_MultiDocYAML(t *testing.T) {
	// Multi-document YAML separated by ---
	input := []byte("name: alpha\n---\nname: beta\n")
	c := NewStructuredChunker(2048)
	chunks, err := c.Chunk("test.yaml", input)
	if err != nil {
		t.Fatal(err)
	}
	// Should produce at least one chunk (likely 2 document chunks since each fits).
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk for multi-doc YAML")
	}
}

func TestStructuredChunker_PathPrefix_ContentEmbedded(t *testing.T) {
	// Verify that the dotted path appears in the Content so the embedding
	// captures structural location.
	var sb strings.Builder
	sb.WriteString("grafana:\n")
	for i := 0; i < 30; i++ {
		sb.WriteString("  key" + string(rune('a'+i%26)) + ": value\n")
	}
	sb.WriteString("prometheus:\n")
	for i := 0; i < 30; i++ {
		sb.WriteString("  key" + string(rune('a'+i%26)) + ": value\n")
	}

	c := NewStructuredChunker(2)
	chunks, err := c.Chunk("values.yaml", []byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}

	for _, ch := range chunks {
		if !strings.Contains(ch.Content, ch.Symbol) {
			t.Errorf("Content for chunk %q does not contain its own symbol", ch.Symbol)
		}
	}
}

// helpers

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func containsPrefix(m map[string]bool, prefix string) bool {
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func symbolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
```

**Step 2: Run the tests (expect failures — implementation not wired yet)**

```bash
go test ./internal/chunker/... -run TestStructuredChunker -v
```

Expected: tests fail because `NewStructuredChunker` doesn't exist yet — this
confirms the test file compiles but the implementation needs writing.

Actually at this point the implementation from Task 2 should exist. Run again:

```bash
go test ./internal/chunker/... -run TestStructuredChunker -v
```

Expected: all `TestStructuredChunker_*` tests PASS.

**Step 3: Commit**

```bash
git add internal/chunker/structured.go internal/chunker/structured_test.go
git commit -m "feat: add StructuredChunker with recursive YAML/JSON key-hierarchy splitting"
```

---

## Task 4: Wire `StructuredChunker` into `DefaultLanguages`

**Files:**

- Modify: `internal/chunker/languages.go`

**Step 1: Replace DataChunker with StructuredChunker**

In `languages.go`, find the block near the bottom that creates `data` and wires
YAML/JSON:

```go
// BEFORE (around line 161-185):
data := NewDataChunker()

return map[string]Chunker{
    ...
    ".yaml": data,
    ".yml":  data,
    ".json": data,
}
```

Change it to:

```go
// Add import: "github.com/ory/agent-index/internal/config"
// at the top of the file (or inline the env read).

maxChunkTokens := config.EnvOrDefaultInt("AGENT_INDEX_MAX_CHUNK_TOKENS", 2048)
structured := NewStructuredChunker(maxChunkTokens)

return map[string]Chunker{
    ...
    ".yaml": structured,
    ".yml":  structured,
    ".json": structured,
}
```

Also add the import `"github.com/ory/agent-index/internal/config"` to the import
block in `languages.go`.

**Step 2: Build and run all unit tests**

```bash
go build ./...
go test ./internal/chunker/... -v
```

Expected: all tests pass, including `TestDefaultLanguages_AllExtensionsPresent`.

**Step 3: Commit**

```bash
git add internal/chunker/languages.go
git commit -m "feat: wire StructuredChunker into DefaultLanguages for yaml/yml/json files"
```

---

## Task 5: Delete stale YAML/JSON snapshots

The YAML and JSON E2E snapshots were already deleted earlier (they were stale
from the old DataChunker). Confirm they are gone:

```bash
ls testdata/snapshots/ | grep -E "YAML|JSON"
```

Expected: no output (files already deleted in a prior step).

If any remain, delete them:

```bash
rm -f testdata/snapshots/TestLang_YAML-* testdata/snapshots/TestLang_JSON-*
```

**Commit the deletion** (if not already committed):

```bash
git add -A testdata/snapshots/
git commit -m "chore: delete stale YAML and JSON snapshots (StructuredChunker changes output)"
```

---

## Task 6: Regenerate snapshots (requires Ollama)

Snapshot regeneration requires Ollama running with the `all-minilm` model.

**Step 1: Verify Ollama is available**

```bash
curl -s http://localhost:11434/api/tags | grep all-minilm
```

Expected: `all-minilm` in the response.

**Step 2: Regenerate YAML and JSON snapshots**

```bash
UPDATE_SNAPSHOTS=true go test -tags e2e -run "TestLang_YAML|TestLang_JSON" -v ./...
```

Expected: 8 new snapshot files created in `testdata/snapshots/`.

**Step 3: Inspect the new snapshots**

```bash
cat testdata/snapshots/TestLang_YAML-Kubernetes_deployment_replicas
```

Expected output (approximately):

- Fewer than 30 results from `kube-prometheus-stack-values.yaml`
- Results should show distinct dotted paths like `grafana`, `prometheus`,
  `alertmanager`
- `Symbol` column shows dotted paths (e.g., `grafana.ingress`,
  `prometheus.alertmanager`)
- `Kind` column shows `(section)`

**Step 4: Commit the new snapshots**

```bash
git add testdata/snapshots/TestLang_YAML-* testdata/snapshots/TestLang_JSON-*
git commit -m "test: regenerate YAML and JSON snapshots with StructuredChunker output"
```

---

## Task 7: Run all E2E tests to verify nothing is broken

```bash
go test -tags e2e ./... -v 2>&1 | tail -50
```

Expected: all tests pass. If snapshot tests for YAML/JSON fail because snapshots
don't match, run `UPDATE_SNAPSHOTS=true` again and review the diff to confirm
the new output is better than the old.

---

## Task 8: Update the analysis document

Update `docs/plans/2026-02-28-snapshot-analysis.md` — the "Changes Implemented"
section — to reflect:

1. `StructuredChunker` implemented (recursive YAML/JSON key-hierarchy splitting)
2. `DataChunker` superseded for `.yaml`/`.yml`/`.json` (still exists for
   isolated use)
3. Patterns 2, 3, 4 addressed by StructuredChunker (fewer distinct chunks per
   file)

```bash
git add docs/plans/2026-02-28-snapshot-analysis.md
git commit -m "docs: update snapshot analysis to reflect StructuredChunker implementation"
```

---

## Verification Checklist

- [ ] `go build ./...` succeeds
- [ ] `go test ./internal/chunker/...` all pass
- [ ] `go test ./...` (unit + integration) all pass
- [ ] `go test -tags e2e -run TestLang_YAML ...` passes with new snapshots
- [ ] `go test -tags e2e -run TestLang_JSON ...` passes with new snapshots
- [ ] YAML "Kubernetes deployment replicas" snapshot shows
      `kube-prometheus-stack-values.yaml` chunks with dotted-path symbols, fewer
      results from single file
- [ ] JSON "TypeScript compiler options" snapshot shows `root[N/M] (document)`
      or key-split results (tsconfig.json still missing — that's a separate
      issue)
