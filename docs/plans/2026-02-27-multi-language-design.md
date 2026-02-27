# Multi-Language Support Design

**Date:** 2026-02-27
**Status:** Approved

## Goal

Extend agent-index-go to index and search code in any language, not just Go. Keep `go/ast` for Go files; use `smacker/go-tree-sitter` for all other languages.

## Architecture

### New files

**`internal/chunker/treesitter.go`** — `TreeSitterChunker`

Wraps a smacker parser and a slice of `chunkRule` (one per declaration kind). Each rule holds a compiled tree-sitter query, the kind string ("function", "method", etc.), and the capture indices for the symbol name and the whole declaration node.

`Chunk(filePath, content)`:
1. Parse content with the language parser
2. Run each rule's query against the tree
3. For each match extract: declaration node (start/end lines → StartLine/EndLine), name node (text → Symbol)
4. Return `[]Chunk` with standard fields

**`internal/chunker/multi.go`** — `MultiChunker`

```go
type MultiChunker struct {
    chunkers map[string]Chunker // extension → Chunker, e.g. ".go" → GoAST
}
```

`Chunk(filePath, content)` extracts the extension and delegates. Unknown extensions return `nil, nil` (no chunks, no error).

**`internal/chunker/languages.go`** — `DefaultLanguages()`

Returns a `map[string]Chunker` pre-wired with:

| Extensions | Parser |
|---|---|
| `.go` | `GoAST` |
| `.ts`, `.tsx` | TreeSitter (TypeScript) |
| `.js`, `.jsx`, `.mjs` | TreeSitter (JavaScript) |
| `.py` | TreeSitter (Python) |
| `.rs` | TreeSitter (Rust) |
| `.rb` | TreeSitter (Ruby) |
| `.java` | TreeSitter (Java) |
| `.c`, `.h` | TreeSitter (C) |
| `.cpp`, `.cc`, `.cxx`, `.hpp` | TreeSitter (C++) |

Also exports `SupportedExtensions() []string` for use in merkle skip.

### Modified files

**`internal/merkle/merkle.go`**

Replace hardcoded `.go` check in `DefaultSkip` with a call to `chunker.SupportedExtensions()`. Extract `makeExtSkip(exts []string) SkipFunc` so the skip function is testable.

**`internal/index/index.go`**

`NewIndexer` replaces `chunker.NewGoAST()` with `chunker.NewMulti(chunker.DefaultLanguages())`.

## Tree-sitter query patterns

Each language needs queries for the kinds it supports. Example for Python:

```scheme
(function_definition name: (identifier) @name) @decl
(class_definition name: (identifier) @name) @decl
```

Rust/TypeScript/JS/Java/C/C++ have analogous patterns per kind.

## Kind mapping per language

| Kind | Go | TS/JS | Python | Rust | Java |
|---|---|---|---|---|---|
| function | ✓ | ✓ | ✓ | ✓ | ✓ |
| method | ✓ | ✓ | ✓ | ✓ | ✓ |
| type / class | ✓ | ✓ | ✓ | ✓ | ✓ |
| interface | ✓ | ✓ | — | ✓ (trait) | ✓ |
| const | ✓ | ✓ | — | ✓ | ✓ |
| var | ✓ | ✓ | — | — | — |

## Testing

- Unit tests for `TreeSitterChunker` per language (fixture snippets in `testdata/`)
- Unit test for `MultiChunker` dispatch and unknown-extension fallback
- Integration test update: `DefaultSkip` now passes non-Go extensions through

## What doesn't change

- `Chunk` struct
- `Chunker` interface
- `Store`, `Indexer.indexWithTree`, MCP tools
- Go AST chunker (untouched)
