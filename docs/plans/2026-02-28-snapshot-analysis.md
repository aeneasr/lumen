# Snapshot Analysis Report — Multi-Language Chunker E2E Tests

**Date:** 2026-02-28
**Scope:** All 53 snapshot files from `TestLang_*` E2E tests across 12 languages

---

## Overview

After reviewing all 53 snapshot files produced by the multi-language E2E snapshot tests, eight recurring problematic patterns were identified. This document records each pattern with severity, examples, root cause, and the chosen fix.

---

## Pattern 1: Split Chunk Flooding (Rust `impl_item`, PHP Class Bodies)

**Severity:** High
**Languages affected:** Rust, PHP

### Description

Rust `impl` blocks and PHP class bodies are captured as a single `impl_item` or `declaration` node, then immediately hit the `splitOversizedChunks` pipeline because a large `impl` block easily exceeds 2048 token limit. A 500-line `impl` becomes 10–15 `impl_item[1/N]` fragments, each named with the same symbol and none carrying a meaningful name.

### Example (Rust)

```
tokio-runtime.rs:1-45   impl_item[1/12] (impl_item)
tokio-runtime.rs:46-90  impl_item[2/12] (impl_item)
...
tokio-runtime.rs:496-540 impl_item[12/12] (impl_item)
```

These 12 chunks drown out actual function results in queries like "Tokio spawn task".

### Root Cause

The Rust tree-sitter query captures `impl_item` (the whole impl block) as a top-level node. Individual methods within impl blocks are captured separately only if the query includes `function_item` inside `impl_item`. The current query captures both, so the impl body duplicates method content.

### Fix

**Drop `impl_item` from the Rust tree-sitter query.** The individual `function_item` captures inside impl blocks already provide method-level granularity. Capturing the outer impl block adds no search value and causes flooding.

---

## Pattern 2: Single File Dominance

**Severity:** High
**Languages affected:** YAML (kube-prometheus-stack), Java (PetClinic)

### Description

One very large file — `kube-prometheus-stack-values.yaml` (5500+ lines) or `PetClinicApplication.java` — dominates every query result, pushing all other files off the 30-result page.

### Example (YAML query "Kubernetes deployment replicas")

```
results: 30
k8s-deployment.yaml:2-2   kind (key)
k8s-load-balancer.yaml:2-2 kind (key)
kube-prometheus-stack-values.yaml:2703-2732  prometheusOperator[4/30] (key)
kube-prometheus-stack-values.yaml:3620-3639  prometheus[8/74]  (key)
... (26 more kube-prometheus-stack lines)
```

Only 2 of 30 results are from the actually relevant `k8s-deployment.yaml`.

### Root Cause

The large file has 74 + 39 + 30 fragment chunks (from `splitOversizedChunks`). Each fragment has a slightly different embedding but all are similar enough to score well for any Kubernetes-related query.

### Fix (YAML)

Solved by Pattern 3's fix (plain text chunking): whole-file chunks for small files + clean line-boundary splits for large files reduces the kube-prometheus-stack flooding. The file still produces many splits, but they are now plain text windows rather than key-based key=boilerplate structures.

### Fix (Java)

Add more diverse Java fixture files. The PetClinic fixture currently has too few files; diversify with at least 5–8 well-chosen source files from different packages.

---

## Pattern 3: YAML Boilerplate Key Pollution

**Severity:** High
**Languages affected:** YAML

### Description

Top-level YAML keys like `kind`, `apiVersion`, `metadata`, `spec` appear as individual 1–2 line chunks. These chunks have near-identical embeddings across all Kubernetes files, making them useless boilerplate that pollutes every YAML query.

### Example

```
k8s-deployment.yaml:2-2   kind (key)
k8s-load-balancer.yaml:2-2 kind (key)
k8s-ingress.yaml:2-2       kind (key)
k8s-deployment.yaml:1-1   apiVersion (key)
k8s-load-balancer.yaml:3-6 metadata (key)
```

Five results from three files, none containing deployment or replica configuration.

### Root Cause

The key-based `DataChunker` splits YAML at every top-level key boundary. Short keys like `kind: Deployment` become 1-line chunks with essentially no semantic content beyond the key name.

### Fix

**Replace key-based chunking with whole-file plain text emission.** The `DataChunker` now emits the full file as a single `document` chunk, and `splitOversizedChunks` divides large files at line boundaries. This produces semantically coherent windows (e.g., a 50-line block of a deployment spec) rather than single-key fragments.

---

## Pattern 4: JSON Line 1-1 Bug for Minified JSON

**Severity:** Medium
**Languages affected:** JSON

### Description

For minified JSON (entire file on one line, like `petstore-openapi.json`), the key-finding logic in `chunkJSON` searches source lines for `"keyName"` but every key is on line 1. Every chunk gets `startLine=1, endLine=1`.

### Example

```
petstore-openapi.json:1-1  servers (key)
petstore-openapi.json:1-1  externalDocs (key)
petstore-openapi.json:1-1  info (key)
petstore-openapi.json:1-1  tags (key)
petstore-openapi.json:1-1  paths (key)
petstore-openapi.json:1-1  openapi (key)
```

All six top-level keys claim to be on line 1, which is technically correct for minified JSON but the resulting chunks all have identical content (the full minified string truncated at some key boundary), making them useless and misleading.

### Fix

Solved by plain text chunking: minified JSON is emitted as a single `document` chunk from line 1 to 1 (which is correct — it is one line). If the file exceeds the token limit, `splitOversizedChunks` handles it.

---

## Pattern 5: Wrong Fixture for Query (JSON tsconfig missing)

**Severity:** Medium
**Languages affected:** JSON

### Description

The query "TypeScript compiler options" returns no `tsconfig.json` results because no tsconfig fixture exists in `testdata/`. Instead, the top results are `package.json` files with TypeScript-related keys (`devDependencies` containing `typescript`).

### Example

```
results: 30
typescript-package.json:41-62  devDependencies[1/2] (key)
babel-package.json:93-100      workspaces (key)
...
```

No `tsconfig.json` appears.

### Fix

**Add a `tsconfig.json` fixture file** to `testdata/json/`. The file `testdata/json/tsconfig.json` was added in a prior commit but was not present when these snapshots were generated. After snapshot regeneration, this query should surface tsconfig results.

---

## Pattern 6: Fixture Library Mixing (JavaScript Express + Fastify)

**Severity:** Low
**Languages affected:** JavaScript

### Description

The JavaScript fixture directory mixes Express (a minimalist HTTP framework) and Fastify (a high-performance HTTP framework) source files. While both are Node.js HTTP frameworks, their APIs differ enough that queries like "Express middleware pipeline" return Fastify-related chunks and vice versa.

### Example

```
results: 30
fastify-app.js:12-45   createFastify (function)   ← wrong library
express-app.js:8-22    createApp (function)
```

### Recommendation

Either keep only one framework per fixture directory, or add clear per-query filtering in the test. Since these are E2E snapshot tests (not unit tests), mixing is acceptable as long as the snapshot is stable. No code change required; document the mix as intentional for coverage breadth.

---

## Pattern 7: Markdown Chapter Over-Splitting

**Severity:** Low
**Languages affected:** Markdown

### Description

The Markdown chunker splits at every heading level. A document with many `###` subheadings produces dozens of 3–5 line chunks. Short sections (e.g., `### Installation\nRun npm install`) become one-sentence chunks with nearly identical embeddings to sibling sections.

### Example

```
README.md:45-47    Installation (heading)
README.md:48-50    Quick Start (heading)
README.md:51-54    Configuration (heading)
```

Each chunk is 2–3 lines. They are semantically near-identical (short imperative text).

### Recommendation

Consider a minimum chunk line count for Markdown: merge adjacent short sections until the combined length exceeds a threshold (e.g., 10 lines). No immediate code change required; severity is low because search quality is acceptable. Track as future improvement.

---

## Pattern 8: Ruby Ambiguous base.rb

**Severity:** Low
**Languages affected:** Ruby

### Description

The Ruby fixture `base.rb` is a generic helper/base class used across multiple gems. Its method names (`initialize`, `call`, `to_s`) are common enough that it matches almost any Ruby query with moderate score. It inflates result counts without improving relevance.

### Example

```
results: 25
base.rb:1-15    Base (class)           score: 0.62
base.rb:16-22   initialize (method)    score: 0.61
...
```

For a query about "ActiveRecord model validation", `base.rb` appears in the top 10 even though it has no Rails-specific code.

### Recommendation

Replace `base.rb` with more specific Ruby fixtures (e.g., an ActiveRecord model, a Rake task, a Sinatra route). No immediate code change required.

---

## Summary Table

| # | Pattern | Severity | Language(s) | Fix |
|---|---------|----------|-------------|-----|
| 1 | Split chunk flooding from `impl_item` | High | Rust, PHP | Drop `impl_item` from Rust query |
| 2 | Single file dominance | High | YAML, Java | Plain text chunking (YAML); more fixture diversity (Java) |
| 3 | YAML boilerplate key pollution | High | YAML | Replace DataChunker with plain text emission |
| 4 | JSON line 1-1 bug for minified JSON | Medium | JSON | Replace DataChunker with plain text emission |
| 5 | Missing tsconfig.json fixture | Medium | JSON | Add tsconfig.json to testdata/json/ |
| 6 | Fixture library mixing | Low | JavaScript | Document as intentional; no code change |
| 7 | Markdown chapter over-splitting | Low | Markdown | Future: min chunk size threshold |
| 8 | Ruby ambiguous base.rb fixture | Low | Ruby | Future: replace with domain-specific fixtures |

---

## Changes Implemented

- **`internal/chunker/data.go`**: Replaced key-based YAML scanner and JSON object parser with whole-file plain text emission. `DataChunker.Chunk()` now returns a single `document` chunk; `splitOversizedChunks` in `internal/index/split.go` handles line-boundary splitting for large files.
- **`internal/chunker/data_test.go`**: Tests updated to verify whole-file `document` chunk emission rather than per-key chunks.
- **`internal/chunker/languages.go` (Rust)**: Removed `impl_item` from the Rust tree-sitter query (Pattern 1). Individual `function_item` captures inside impl blocks already cover all methods; the whole-impl-block capture only caused split-chunk flooding.
- **`internal/chunker/languages.go` (PHP)**: Removed `class_declaration` from the PHP tree-sitter query (Pattern 1). Individual `method_declaration` captures already cover all methods; whole-class capture caused 82+ split-chunk flooding.
- **`internal/chunker/structured.go`**: New `StructuredChunker` replaces `DataChunker` for `.yaml`/`.yml`/`.json` files (Patterns 2, 3, 4). Uses `gopkg.in/yaml.v3` to parse both YAML and JSON into a node tree, then recursively descends: subtrees that fit the token budget are emitted as one `"section"` chunk with a `# path: dotted.key.path` prefix in `Content`; oversized subtrees recurse into their children. Small files (whole file ≤ budget) pass through as a single `"document"` chunk. `splitOversizedChunks` remains the backstop for leaf nodes that cannot be subdivided further.
- **`internal/chunker/structured_test.go`**: Unit tests for `StructuredChunker` covering small/large YAML, small/large JSON, empty files, multi-document YAML, and path-in-content invariant.
- **Snapshots deleted**: All 8 YAML and JSON snapshot files deleted; 10 Rust and PHP snapshot files deleted. Must be regenerated with Ollama running (`UPDATE_SNAPSHOTS=true go test -tags e2e ./...`).
