# Multi-Language Fixtures & Snapshot Tests Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to
> implement this plan task-by-task.

**Goal:** Add PHP/YAML+JSON/Markdown chunkers, vendor real-world fixtures (30-50
files per language), write cupaloy snapshot E2E tests per language, and raise
default result limits with score-based filtering.

**Architecture:** New chunkers (PHP via tree-sitter, Markdown/YAML+JSON via
plain-text scanning) are added to `internal/chunker/`. A shell script downloads
real files from well-known open-source repos into `testdata/fixtures/<lang>/`. A
new E2E test file `e2e_lang_test.go` uses `cupaloy` to snapshot-test 5–8 queries
per language. Oversized chunks from new chunkers pass through the existing
`split.go` pipeline unchanged.

**Tech Stack:** Go 1.26, tree-sitter (PHP), `encoding/json` (JSON chunker),
`github.com/bradleyjkemp/cupaloy/v2` (snapshots), existing
`internal/index/split.go` for oversized-chunk splitting.

**GitHub repo:** `github.com/aeneasr/agent-index-go` — badge URL base:
`https://github.com/aeneasr/agent-index-go/actions/workflows/ci.yml`

---

## Task 1: Add cupaloy dependency

**Files:** `go.mod`, `go.sum`

**Step 1:** Add dependency

```bash
go get github.com/bradleyjkemp/cupaloy/v2
```

**Step 2:** Verify it appears in go.mod

```bash
grep cupaloy go.mod
```

Expected: `github.com/bradleyjkemp/cupaloy/v2 v2.8.0` (or later)

**Step 3:** Commit

```bash
git add go.mod go.sum
git commit -m "chore: add cupaloy snapshot testing dependency"
```

---

## Task 2: PHP chunker

**Files:**

- Modify: `internal/chunker/languages.go`

**Step 1:** Add PHP import at top of `internal/chunker/languages.go`:

```go
sitter_php "github.com/smacker/go-tree-sitter/php"
```

**Step 2:** Add PHP chunker definition inside `DefaultLanguages()`, before the
`goChunker := NewGoAST()` line:

```go
php := mustTreeSitterChunker(LanguageDef{
    Language: sitter_php.GetLanguage(),
    Queries: []QueryDef{
        {Pattern: `(function_definition name: (name) @name) @decl`, Kind: "function"},
        {Pattern: `(class_declaration name: (name) @name) @decl`, Kind: "type"},
        {Pattern: `(interface_declaration name: (name) @name) @decl`, Kind: "interface"},
        {Pattern: `(trait_declaration name: (name) @name) @decl`, Kind: "type"},
        {Pattern: `(method_declaration name: (name) @name) @decl`, Kind: "method"},
    },
})
```

**Step 3:** Add to the return map and to `supportedExtensions`:

```go
// In supportedExtensions slice:
".php",

// In DefaultLanguages() return map:
".php": php,
```

**Step 4:** Build to verify queries compile:

```bash
CGO_ENABLED=1 go build ./...
```

Expected: no errors. If a query pattern errors, check tree-sitter PHP node type
names with `sitter_php.GetLanguage()`.

**Step 5:** Commit

```bash
git add internal/chunker/languages.go
git commit -m "feat: add PHP tree-sitter chunker"
```

---

## Task 3: Markdown chunker

**Files:**

- Create: `internal/chunker/markdown.go`
- Create: `internal/chunker/markdown_test.go`

**Step 1:** Create `internal/chunker/markdown.go`:

```go
// Copyright 2026 Aeneas Rekkas
//
// Licensed under the Apache License, Version 2.0 (the "License");
// ...

package chunker

import (
	"strings"
)

// MarkdownChunker splits Markdown/MDX files into chunks by ATX heading sections.
// Each heading (# / ## / ###) plus its body becomes one chunk.
// Content before the first heading is emitted as a single "preamble" chunk if non-empty.
// Oversized chunks pass through to the split.go pipeline.
type MarkdownChunker struct{}

// NewMarkdownChunker returns a new MarkdownChunker.
func NewMarkdownChunker() *MarkdownChunker { return &MarkdownChunker{} }

// Chunk implements Chunker for Markdown files.
func (c *MarkdownChunker) Chunk(filePath string, content []byte) ([]Chunk, error) {
	lines := strings.Split(string(content), "\n")
	var chunks []Chunk

	type section struct {
		symbol    string
		startLine int
		lines     []string
	}

	var current *section
	flush := func(endLine int) {
		if current == nil {
			return
		}
		body := strings.TrimSpace(strings.Join(current.lines, "\n"))
		if body == "" {
			current = nil
			return
		}
		chunks = append(chunks, makeChunk(filePath, current.symbol, "section", current.startLine, endLine, body))
		current = nil
	}

	for i, line := range lines {
		lineNum := i + 1
		// Detect ATX headings: # Foo, ## Foo, ### Foo (up to ###)
		if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			flush(lineNum - 1)
			heading := strings.TrimLeft(line, "#")
			heading = strings.TrimSpace(heading)
			current = &section{symbol: heading, startLine: lineNum, lines: []string{line}}
			continue
		}
		if current != nil {
			current.lines = append(current.lines, line)
		} else {
			// preamble: content before first heading
			if strings.TrimSpace(line) != "" {
				if len(chunks) == 0 {
					current = &section{symbol: "preamble", startLine: lineNum, lines: []string{line}}
				}
			}
		}
	}
	flush(len(lines))

	return chunks, nil
}
```

**Step 2:** Create `internal/chunker/markdown_test.go`:

```go
// Copyright 2026 Aeneas Rekkas
// ...

package chunker

import (
	"testing"
)

func TestMarkdownChunker_Basic(t *testing.T) {
	src := `# Introduction
This is intro text.

## Installation

Run this command.

### Advanced
More details.
`
	c := NewMarkdownChunker()
	chunks, err := c.Chunk("docs/readme.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if chunks[0].Symbol != "Introduction" {
		t.Errorf("chunk[0].Symbol = %q, want Introduction", chunks[0].Symbol)
	}
	if chunks[0].Kind != "section" {
		t.Errorf("chunk[0].Kind = %q, want section", chunks[0].Kind)
	}
	if chunks[1].Symbol != "Installation" {
		t.Errorf("chunk[1].Symbol = %q, want Installation", chunks[1].Symbol)
	}
}

func TestMarkdownChunker_EmptyFile(t *testing.T) {
	c := NewMarkdownChunker()
	chunks, err := c.Chunk("empty.md", []byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}
```

**Step 3:** Run tests:

```bash
CGO_ENABLED=1 go test ./internal/chunker/... -run TestMarkdownChunker -v
```

Expected: PASS

**Step 4:** Register in `languages.go` — add to `SupportedExtensions` and
`DefaultLanguages()`:

```go
// supportedExtensions:
".md", ".mdx",

// DefaultLanguages() return map:
".md":  NewMarkdownChunker(),
".mdx": NewMarkdownChunker(),
```

**Step 5:** Commit

```bash
git add internal/chunker/markdown.go internal/chunker/markdown_test.go internal/chunker/languages.go
git commit -m "feat: add Markdown/MDX chunker splitting by ATX headings"
```

---

## Task 4: Data chunker (YAML + JSON)

**Files:**

- Create: `internal/chunker/data.go`
- Create: `internal/chunker/data_test.go`

**Step 1:** Create `internal/chunker/data.go`:

```go
// Copyright 2026 Aeneas Rekkas
// ...

package chunker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// DataChunker chunks YAML and JSON files by their top-level keys.
// Each top-level key and its associated value block becomes one chunk.
// Oversized chunks pass through to the split.go pipeline unchanged.
type DataChunker struct{}

// NewDataChunker returns a new DataChunker.
func NewDataChunker() *DataChunker { return &DataChunker{} }

var yamlTopLevelKey = regexp.MustCompile(`^([a-zA-Z_"'\-][a-zA-Z0-9_"'\-]*)\s*:`)

// Chunk implements Chunker. Dispatches to YAML or JSON based on file extension.
func (c *DataChunker) Chunk(filePath string, content []byte) ([]Chunk, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".json":
		return c.chunkJSON(filePath, content)
	default: // .yaml, .yml
		return c.chunkYAML(filePath, content)
	}
}

func (c *DataChunker) chunkYAML(filePath string, content []byte) ([]Chunk, error) {
	type section struct {
		key       string
		startLine int
		lines     []string
	}

	var chunks []Chunk
	var current *section

	flush := func(endLine int) {
		if current == nil {
			return
		}
		body := strings.Join(current.lines, "\n")
		if strings.TrimSpace(body) == "" {
			current = nil
			return
		}
		chunks = append(chunks, makeChunk(filePath, current.key, "key", current.startLine, endLine, body))
		current = nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Top-level keys: no leading whitespace, matches key pattern
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "-") {
			if m := yamlTopLevelKey.FindStringSubmatch(line); m != nil {
				flush(lineNum - 1)
				key := strings.Trim(m[1], `"'`)
				current = &section{key: key, startLine: lineNum, lines: []string{line}}
				continue
			}
		}
		if current != nil {
			current.lines = append(current.lines, line)
		}
	}
	flush(lineNum)

	return chunks, nil
}

func (c *DataChunker) chunkJSON(filePath string, content []byte) ([]Chunk, error) {
	// We need to locate each top-level key's byte span in the raw content.
	// Strategy: decode as map[string]json.RawMessage to get values,
	// then scan the source for each key's start/end line.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(content, &obj); err != nil {
		// Not a JSON object (could be array/scalar) — emit as single chunk.
		if len(content) > 0 {
			lines := strings.Count(string(content), "\n") + 1
			return []Chunk{makeChunk(filePath, "root", "key", 1, lines, string(content))}, nil
		}
		return nil, nil
	}

	// For each top-level key, find its line range via byte scanning.
	lines := strings.Split(string(content), "\n")
	var chunks []Chunk
	for key, raw := range obj {
		// Find the first line containing `"<key>":`
		searchKey := `"` + key + `"`
		startLine, endLine := 1, len(lines)
		for i, l := range lines {
			if strings.Contains(l, searchKey) {
				startLine = i + 1
				// End: count newlines in the raw value + a couple lines for structure
				rawLines := strings.Count(string(raw), "\n")
				end := startLine + rawLines + 1
				if end > len(lines) {
					end = len(lines)
				}
				endLine = end
				break
			}
		}
		// Build snippet: key line + raw value
		body := searchKey + ": " + string(raw)
		chunks = append(chunks, makeChunk(filePath, key, "key", startLine, endLine, body))
	}

	return chunks, nil
}
```

**Step 2:** Create `internal/chunker/data_test.go`:

```go
// Copyright 2026 Aeneas Rekkas
// ...

package chunker

import (
	"testing"
)

func TestDataChunker_YAML(t *testing.T) {
	src := `name: my-app
version: 1.0.0
dependencies:
  - foo
  - bar
config:
  host: localhost
  port: 8080
`
	c := NewDataChunker()
	chunks, err := c.Chunk("config.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d: %v", len(chunks), chunks)
	}
	// All should be kind "key"
	for _, ch := range chunks {
		if ch.Kind != "key" {
			t.Errorf("chunk %q: Kind = %q, want key", ch.Symbol, ch.Kind)
		}
	}
}

func TestDataChunker_JSON(t *testing.T) {
	src := `{
  "name": "my-app",
  "version": "1.0.0",
  "scripts": {
    "build": "tsc",
    "test": "jest"
  }
}`
	c := NewDataChunker()
	chunks, err := c.Chunk("package.json", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks, got none")
	}
	for _, ch := range chunks {
		if ch.Kind != "key" {
			t.Errorf("chunk %q: Kind = %q, want key", ch.Symbol, ch.Kind)
		}
	}
}

func TestDataChunker_EmptyYAML(t *testing.T) {
	c := NewDataChunker()
	chunks, err := c.Chunk("empty.yaml", []byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}
```

**Step 3:** Run tests:

```bash
CGO_ENABLED=1 go test ./internal/chunker/... -run TestDataChunker -v
```

Expected: PASS

**Step 4:** Register in `languages.go`:

```go
// supportedExtensions:
".yaml", ".yml", ".json",

// DefaultLanguages() return map:
".yaml": NewDataChunker(),
".yml":  NewDataChunker(),
".json": NewDataChunker(),
```

**Step 5:** Build and test:

```bash
CGO_ENABLED=1 go build ./... && CGO_ENABLED=1 go test ./...
```

**Step 6:** Commit

```bash
git add internal/chunker/data.go internal/chunker/data_test.go internal/chunker/languages.go
git commit -m "feat: add YAML+JSON data chunker splitting by top-level keys"
```

---

## Task 5: Fixture download script

**Files:**

- Create: `scripts/download-fixtures.sh`

This script downloads 30-50 real source files per language from well-known
MIT/Apache-licensed GitHub repos. Run it once; commit results.

**Step 1:** Create `scripts/download-fixtures.sh`:

```bash
#!/usr/bin/env bash
# Downloads fixture files for E2E multi-language snapshot tests.
# Usage: bash scripts/download-fixtures.sh
# Requires: curl, git

set -euo pipefail

FIXTURES="testdata/fixtures"
GH_RAW="https://raw.githubusercontent.com"

dl() {
  local lang="$1" repo="$2" ref="$3" path="$4"
  local dest="$FIXTURES/$lang/$(basename "$path")"
  mkdir -p "$FIXTURES/$lang"
  echo "  $lang: $path"
  curl -fsSL "$GH_RAW/$repo/$ref/$path" -o "$dest"
}

# ── Go: prometheus/prometheus ──────────────────────────────────────────────────
echo "==> Go (prometheus/prometheus)"
REPO="prometheus/prometheus"; REF="main"
for f in \
  model/labels/labels.go model/labels/matcher.go \
  model/relabel/relabel.go \
  rules/manager.go rules/alerting.go rules/recording.go \
  web/api/v1/api.go \
  promql/engine.go promql/eval.go promql/functions.go promql/parser/ast.go \
  storage/interface.go storage/merge.go \
  tsdb/head.go tsdb/compact.go tsdb/db.go \
  discovery/manager.go discovery/targetgroup/targetgroup.go \
  config/config.go notifier/notifier.go \
  scrape/manager.go scrape/scrape.go \
  util/strutil/strutil.go util/gate/gate.go \
; do dl go "$REPO" "$REF" "$f"; done

# ── Java: spring-projects/spring-petclinic ─────────────────────────────────────
echo "==> Java (spring-petclinic)"
REPO="spring-projects/spring-petclinic"; REF="main"
for f in \
  src/main/java/org/springframework/samples/petclinic/PetClinicApplication.java \
  src/main/java/org/springframework/samples/petclinic/owner/Owner.java \
  src/main/java/org/springframework/samples/petclinic/owner/OwnerController.java \
  src/main/java/org/springframework/samples/petclinic/owner/OwnerRepository.java \
  src/main/java/org/springframework/samples/petclinic/owner/Pet.java \
  src/main/java/org/springframework/samples/petclinic/owner/PetController.java \
  src/main/java/org/springframework/samples/petclinic/owner/PetType.java \
  src/main/java/org/springframework/samples/petclinic/owner/Visit.java \
  src/main/java/org/springframework/samples/petclinic/owner/VisitController.java \
  src/main/java/org/springframework/samples/petclinic/system/CacheConfiguration.java \
  src/main/java/org/springframework/samples/petclinic/system/CrashController.java \
  src/main/java/org/springframework/samples/petclinic/vet/Specialty.java \
  src/main/java/org/springframework/samples/petclinic/vet/Vet.java \
  src/main/java/org/springframework/samples/petclinic/vet/VetController.java \
  src/main/java/org/springframework/samples/petclinic/vet/VetRepository.java \
  src/main/java/org/springframework/samples/petclinic/vet/Vets.java \
; do dl java "$REPO" "$REF" "$f"; done
# Also pull from spring-framework for more coverage
REPO="spring-projects/spring-framework"; REF="main"
for f in \
  spring-web/src/main/java/org/springframework/web/bind/annotation/RequestMapping.java \
  spring-web/src/main/java/org/springframework/web/bind/annotation/RestController.java \
  spring-web/src/main/java/org/springframework/web/filter/OncePerRequestFilter.java \
  spring-context/src/main/java/org/springframework/context/ApplicationContext.java \
  spring-context/src/main/java/org/springframework/context/ApplicationEvent.java \
  spring-core/src/main/java/org/springframework/core/env/Environment.java \
  spring-core/src/main/java/org/springframework/core/io/Resource.java \
  spring-beans/src/main/java/org/springframework/beans/factory/BeanFactory.java \
  spring-beans/src/main/java/org/springframework/beans/factory/annotation/Autowired.java \
  spring-tx/src/main/java/org/springframework/transaction/annotation/Transactional.java \
  spring-data-commons/src/main/java/org/springframework/data/repository/CrudRepository.java \
; do dl java "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── PHP: laravel/laravel skeleton ──────────────────────────────────────────────
echo "==> PHP (laravel/laravel + laravel/framework)"
REPO="laravel/laravel"; REF="11.x"
for f in \
  app/Http/Controllers/Controller.php \
  app/Http/Middleware/RedirectIfAuthenticated.php \
  app/Models/User.php \
  app/Providers/AppServiceProvider.php \
  bootstrap/app.php \
  routes/web.php routes/api.php \
; do dl php "$REPO" "$REF" "$f" 2>/dev/null || true; done
# Pull from laravel/framework for rich source
REPO="laravel/framework"; REF="11.x"
for f in \
  src/Illuminate/Auth/AuthManager.php \
  src/Illuminate/Auth/Guard.php \
  src/Illuminate/Cache/CacheManager.php \
  src/Illuminate/Cache/Repository.php \
  src/Illuminate/Database/Connection.php \
  src/Illuminate/Database/Eloquent/Model.php \
  src/Illuminate/Database/Eloquent/Builder.php \
  src/Illuminate/Database/Eloquent/Relations/HasMany.php \
  src/Illuminate/Database/Eloquent/Relations/BelongsTo.php \
  src/Illuminate/Http/Request.php \
  src/Illuminate/Http/Response.php \
  src/Illuminate/Queue/QueueManager.php \
  src/Illuminate/Queue/Worker.php \
  src/Illuminate/Routing/Router.php \
  src/Illuminate/Routing/Route.php \
  src/Illuminate/Support/Collection.php \
  src/Illuminate/Support/Str.php \
  src/Illuminate/Support/Arr.php \
  src/Illuminate/Validation/Validator.php \
  src/Illuminate/View/Factory.php \
  src/Illuminate/Container/Container.php \
  src/Illuminate/Events/Dispatcher.php \
  src/Illuminate/Log/Logger.php \
  src/Illuminate/Mail/Mailer.php \
  src/Illuminate/Notifications/NotificationSender.php \
  src/Illuminate/Pipeline/Pipeline.php \
  src/Illuminate/Session/Store.php \
; do dl php "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── JavaScript: expressjs/express ─────────────────────────────────────────────
echo "==> JavaScript (expressjs/express)"
REPO="expressjs/express"; REF="master"
for f in \
  lib/application.js lib/express.js lib/request.js lib/response.js \
  lib/router/index.js lib/router/layer.js lib/router/route.js \
  lib/middleware/init.js lib/middleware/query.js \
  lib/utils.js lib/view.js \
; do dl js "$REPO" "$REF" "$f"; done
# node/node for more JS coverage
REPO="nodejs/node"; REF="main"
for f in \
  lib/http.js lib/https.js lib/url.js lib/path.js \
  lib/fs.js lib/stream.js lib/events.js lib/util.js \
  lib/net.js lib/crypto.js lib/os.js lib/buffer.js \
  lib/child_process.js lib/cluster.js lib/dns.js \
  lib/readline.js lib/repl.js \
; do dl js "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── TypeScript: microsoft/vscode ──────────────────────────────────────────────
echo "==> TypeScript (microsoft/vscode src/vs/base)"
REPO="microsoft/vscode"; REF="main"
for f in \
  src/vs/base/common/arrays.ts \
  src/vs/base/common/async.ts \
  src/vs/base/common/buffer.ts \
  src/vs/base/common/cancellation.ts \
  src/vs/base/common/collections.ts \
  src/vs/base/common/color.ts \
  src/vs/base/common/decorators.ts \
  src/vs/base/common/diff/diff.ts \
  src/vs/base/common/errors.ts \
  src/vs/base/common/event.ts \
  src/vs/base/common/glob.ts \
  src/vs/base/common/hash.ts \
  src/vs/base/common/iterator.ts \
  src/vs/base/common/json.ts \
  src/vs/base/common/lazy.ts \
  src/vs/base/common/lifecycle.ts \
  src/vs/base/common/map.ts \
  src/vs/base/common/network.ts \
  src/vs/base/common/objects.ts \
  src/vs/base/common/path.ts \
  src/vs/base/common/platform.ts \
  src/vs/base/common/process.ts \
  src/vs/base/common/resources.ts \
  src/vs/base/common/sequence.ts \
  src/vs/base/common/stream.ts \
  src/vs/base/common/strings.ts \
  src/vs/base/common/types.ts \
  src/vs/base/common/uri.ts \
  src/vs/base/common/uuid.ts \
  src/vs/base/node/pfs.ts \
  src/vs/base/node/zip.ts \
  src/vs/editor/common/model.ts \
  src/vs/editor/common/languages.ts \
  src/vs/workbench/services/editor/common/editorService.ts \
  src/vs/platform/log/common/log.ts \
  src/vs/platform/files/common/files.ts \
  src/vs/platform/storage/common/storage.ts \
  src/vs/platform/configuration/common/configuration.ts \
; do dl ts "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── Ruby: sinatra/sinatra ─────────────────────────────────────────────────────
echo "==> Ruby (sinatra/sinatra)"
REPO="sinatra/sinatra"; REF="main"
for f in \
  lib/sinatra/base.rb \
  lib/sinatra/helpers.rb \
  lib/sinatra/indifferent_hash.rb \
  lib/sinatra/main.rb \
  lib/sinatra/router.rb \
  lib/sinatra/show_exceptions.rb \
  lib/sinatra/version.rb \
; do dl ruby "$REPO" "$REF" "$f" 2>/dev/null || true; done
REPO="rails/rails"; REF="main"
for f in \
  actionpack/lib/action_controller/base.rb \
  actionpack/lib/action_controller/metal.rb \
  actionpack/lib/action_dispatch/routing/router.rb \
  actionpack/lib/action_dispatch/routing/route_set.rb \
  activerecord/lib/active_record/base.rb \
  activerecord/lib/active_record/connection_adapters/abstract_adapter.rb \
  activerecord/lib/active_record/relation.rb \
  activerecord/lib/active_record/associations.rb \
  activerecord/lib/active_record/callbacks.rb \
  activerecord/lib/active_record/validations.rb \
  activesupport/lib/active_support/concern.rb \
  activesupport/lib/active_support/core_ext/array/wrap.rb \
  activesupport/lib/active_support/inflector/methods.rb \
  activesupport/lib/active_support/cache.rb \
  activesupport/lib/active_support/notifications.rb \
  railties/lib/rails/application.rb \
  railties/lib/rails/engine.rb \
  railties/lib/rails/generators/base.rb \
  actionmailer/lib/action_mailer/base.rb \
  activejob/lib/active_job/base.rb \
  activestorage/app/models/active_storage/blob.rb \
  actioncable/lib/action_cable/connection/base.rb \
  actioncable/lib/action_cable/channel/base.rb \
; do dl ruby "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── Python: pallets/flask ─────────────────────────────────────────────────────
echo "==> Python (pallets/flask)"
REPO="pallets/flask"; REF="main"
for f in \
  src/flask/__init__.py \
  src/flask/app.py \
  src/flask/blueprints.py \
  src/flask/cli.py \
  src/flask/config.py \
  src/flask/ctx.py \
  src/flask/debughelpers.py \
  src/flask/globals.py \
  src/flask/helpers.py \
  src/flask/logging.py \
  src/flask/scaffold.py \
  src/flask/sessions.py \
  src/flask/signals.py \
  src/flask/templating.py \
  src/flask/testing.py \
  src/flask/typing.py \
  src/flask/views.py \
  src/flask/wrappers.py \
; do dl python "$REPO" "$REF" "$f" 2>/dev/null || true; done
REPO="django/django"; REF="main"
for f in \
  django/db/models/base.py \
  django/db/models/manager.py \
  django/db/models/query.py \
  django/db/models/fields/__init__.py \
  django/views/generic/base.py \
  django/views/generic/detail.py \
  django/views/generic/list.py \
  django/contrib/auth/models.py \
  django/contrib/auth/views.py \
  django/contrib/auth/backends.py \
  django/core/exceptions.py \
  django/http/request.py \
  django/http/response.py \
  django/middleware/common.py \
  django/urls/resolvers.py \
; do dl python "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── Rust: BurntSushi/ripgrep ──────────────────────────────────────────────────
echo "==> Rust (BurntSushi/ripgrep)"
REPO="BurntSushi/ripgrep"; REF="master"
for f in \
  crates/core/app.rs \
  crates/core/args.rs \
  crates/core/logger.rs \
  crates/core/search.rs \
  crates/grep/src/lib.rs \
  crates/grep-cli/src/lib.rs \
  crates/grep-matcher/src/lib.rs \
  crates/grep-printer/src/lib.rs \
  crates/grep-regex/src/lib.rs \
  crates/grep-searcher/src/lib.rs \
; do dl rust "$REPO" "$REF" "$f" 2>/dev/null || true; done
REPO="tokio-rs/tokio"; REF="master"
for f in \
  tokio/src/runtime/mod.rs \
  tokio/src/runtime/builder.rs \
  tokio/src/runtime/handle.rs \
  tokio/src/task/mod.rs \
  tokio/src/task/spawn.rs \
  tokio/src/sync/mutex.rs \
  tokio/src/sync/rwlock.rs \
  tokio/src/sync/mpsc/mod.rs \
  tokio/src/sync/oneshot.rs \
  tokio/src/time/mod.rs \
  tokio/src/time/sleep.rs \
  tokio/src/net/tcp/listener.rs \
  tokio/src/net/tcp/stream.rs \
  tokio/src/io/mod.rs \
  tokio/src/io/util/read.rs \
  tokio/src/fs/mod.rs \
  tokio/src/process/mod.rs \
; do dl rust "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── YAML: kubernetes manifests + GitHub Actions ───────────────────────────────
echo "==> YAML (k8s samples + GH Actions)"
REPO="kubernetes/examples"; REF="master"
for f in \
  guestbook/all-in-one/guestbook-all-in-one.yaml \
  mysql-wordpress-pd/mysql-deployment.yaml \
  mysql-wordpress-pd/wordpress-deployment.yaml \
  volumes/nfs/nfs-server-rc.yaml \
  staging/cassandra/cassandra-statefulset.yaml \
  staging/cassandra/cassandra-service.yaml \
; do dl yaml "$REPO" "$REF" "$f" 2>/dev/null || true; done
# Actions workflows from popular repos
for repo_ref_path in \
  "actions/runner:main:.github/workflows/build.yml" \
  "cli/cli:trunk:.github/workflows/check.yml" \
  "gohugoio/hugo:master:.github/workflows/test.yml" \
  "pallets/flask:main:.github/workflows/tests.yaml" \
  "BurntSushi/ripgrep:master:.github/workflows/ci.yml" \
; do
  IFS=":" read -r repo ref path <<< "$repo_ref_path"
  dest_name="$(echo "$repo" | tr '/' '-')-$(basename "$path")"
  mkdir -p "$FIXTURES/yaml"
  curl -fsSL "$GH_RAW/$repo/$ref/$path" -o "$FIXTURES/yaml/$dest_name" 2>/dev/null || true
done
# Docker Compose files
REPO="docker/awesome-compose"; REF="master"
for f in \
  nginx-flask-mongo/compose.yaml \
  react-java-mysql/compose.yaml \
  flask-redis/compose.yaml \
  wordpress-mysql/compose.yaml \
; do dl yaml "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── Markdown: docs from well-known repos ─────────────────────────────────────
echo "==> Markdown (vercel/next.js + remix docs)"
REPO="vercel/next.js"; REF="canary"
for f in \
  docs/01-app/01-getting-started/01-installation.mdx \
  docs/01-app/01-getting-started/02-project-structure.mdx \
  docs/01-app/02-guides/forms.mdx \
  docs/01-app/02-guides/authentication.mdx \
  docs/01-app/03-api-reference/01-directives/use-cache.mdx \
  docs/01-app/03-api-reference/02-components/image.mdx \
  docs/01-app/03-api-reference/02-components/link.mdx \
  docs/01-app/03-api-reference/05-config/01-next-config-js/output.mdx \
  docs/02-pages/01-getting-started/01-installation.mdx \
  docs/02-pages/04-api-reference/01-components/image-legacy.mdx \
; do dl md "$REPO" "$REF" "$f" 2>/dev/null || true; done
REPO="golang/go"; REF="master"
for f in \
  doc/diagnostics.html \
; do true; done  # skip html, use wiki
REPO="rust-lang/book"; REF="main"
for f in \
  src/ch01-01-installation.md \
  src/ch01-02-hello-world.md \
  src/ch02-00-guessing-game-tutorial.md \
  src/ch03-01-variables-and-mutability.md \
  src/ch03-02-data-types.md \
  src/ch03-03-how-functions-work.md \
  src/ch04-01-what-is-ownership.md \
  src/ch04-02-references-and-borrowing.md \
  src/ch05-01-defining-structs.md \
  src/ch06-01-defining-an-enum.md \
  src/ch07-01-packages-and-crates.md \
  src/ch08-01-vectors.md \
  src/ch09-01-unrecoverable-errors-with-panic.md \
  src/ch10-01-syntax.md \
  src/ch11-01-writing-tests.md \
  src/ch12-01-accepting-command-line-arguments.md \
  src/ch13-01-closures.md \
  src/ch15-01-box.md \
  src/ch16-01-threads.md \
  src/ch17-01-futures-and-syntax.md \
; do dl md "$REPO" "$REF" "$f" 2>/dev/null || true; done

# ── JSON: package.json files + OpenAPI specs ──────────────────────────────────
echo "==> JSON (package.json + OpenAPI specs)"
for repo_ref in \
  "expressjs/express:master" \
  "vercel/next.js:canary" \
  "facebook/react:main" \
  "microsoft/TypeScript:main" \
  "eslint/eslint:main" \
  "prettier/prettier:main" \
; do
  IFS=":" read -r repo ref <<< "$repo_ref"
  name="$(echo "$repo" | tr '/' '-')-package.json"
  curl -fsSL "$GH_RAW/$repo/$ref/package.json" -o "$FIXTURES/json/$name" 2>/dev/null || true
done
# OpenAPI specs from APIs-guru
REPO="APIs-guru/openapi-directory"; REF="main"
for f in \
  APIs/github.com/api.github.com/1.1.4/openapi.json \
; do dl json "$REPO" "$REF" "$f" 2>/dev/null || true; done
# tsconfig files
for repo_ref in \
  "microsoft/TypeScript:main" \
  "vercel/next.js:canary" \
; do
  IFS=":" read -r repo ref <<< "$repo_ref"
  name="$(echo "$repo" | tr '/' '-')-tsconfig.json"
  curl -fsSL "$GH_RAW/$repo/$ref/tsconfig.json" -o "$FIXTURES/json/$name" 2>/dev/null || true
done

echo "==> Done. Files in testdata/fixtures/:"
find testdata/fixtures -type f | sort | awk -F/ '{print $3}' | sort | uniq -c | sort -rn
```

**Step 2:** Make executable and run:

```bash
chmod +x scripts/download-fixtures.sh
bash scripts/download-fixtures.sh
```

**Step 3:** Check counts per language (want 20+ per language min):

```bash
find testdata/fixtures -type f | awk -F/ '{print $3}' | sort | uniq -c | sort -rn
```

**Step 4:** Write SOURCES.md for each language (example for Go):

```bash
cat > testdata/fixtures/go/SOURCES.md << 'EOF'
# Go Fixture Sources

Files in this directory are vendored from:

- **prometheus/prometheus** (Apache 2.0) — https://github.com/prometheus/prometheus
  - Commit: main branch as of 2026-02-28

These files are used as test fixtures for semantic code search testing.
EOF
```

Write similar SOURCES.md for each language directory.

**Step 5:** Commit

```bash
git add testdata/fixtures/ scripts/
git commit -m "test: add multi-language fixture files from real open-source projects"
```

---

## Task 6: Snapshot E2E tests per language

**Files:**

- Create: `e2e_lang_test.go`

This file uses `cupaloy` to snapshot-test search results per language. Snapshots
are stored in `.snapshots/` relative to the test file (cupaloy default). Scores
are stripped before snapshotting since they vary by model.

**Step 1:** Create `e2e_lang_test.go`:

```go
// Copyright 2026 Aeneas Rekkas
// ...

//go:build e2e

package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bradleyjkemp/cupaloy/v2"
)

// snapshotter is the cupaloy instance used for all language snapshot tests.
// Snapshots live in testdata/snapshots/ for clarity.
var snapshotter = cupaloy.New(cupaloy.EnvVariableName("UPDATE_SNAPSHOTS"))

// fixturesDir returns the absolute path to testdata/fixtures/<lang>.
func fixturesDir(lang string) string {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	return filepath.Join(root, "testdata", "fixtures", lang)
}

// langQuery runs a search query against a language fixture dir and returns
// a stable snapshot string (scores omitted, results sorted by file+symbol).
func langQuery(t *testing.T, session interface{ /* *mcp.ClientSession */ }, lang, query string, limit int) string {
	t.Helper()
	dir := fixturesDir(lang)
	out := callSearch(t, session.(*mcpSession), map[string]any{
		"query":     query,
		"path":      dir,
		"limit":     limit,
		"min_score": -1.0, // return everything; snapshot captures shape not scores
	})
	return formatSnapshot(lang, query, out)
}

// formatSnapshot renders results without scores for stable snapshotting.
func formatSnapshot(lang, query string, out semanticSearchOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "lang: %s\nquery: %s\nresults: %d\n\n", lang, query, len(out.Results))
	for _, r := range out.Results {
		fmt.Fprintf(&b, "── %s:%d-%d  %s (%s) ──\n", r.FilePath, r.StartLine, r.EndLine, r.Symbol, r.Kind)
	}
	return b.String()
}

// NOTE: mcpSession is a type alias used here for clarity.
// The actual session type is *mcp.ClientSession from startServer().

func runLangSnapshots(t *testing.T, lang string, queries []string) {
	t.Helper()
	session := startServer(t)
	for _, q := range queries {
		q := q
		t.Run(strings.ReplaceAll(q, " ", "_"), func(t *testing.T) {
			result := langQuery(t, session, lang, q, 50)
			snapshotter.SnapshotT(t, result)
		})
	}
}

func TestLang_Go(t *testing.T) {
	runLangSnapshots(t, "go", []string{
		"HTTP request handler",
		"authentication token validation",
		"database connection pool",
		"time series storage",
		"error handling middleware",
		"configuration loading",
		"metric collection and scraping",
	})
}

func TestLang_Java(t *testing.T) {
	runLangSnapshots(t, "java", []string{
		"pet owner repository",
		"REST controller request mapping",
		"JPA entity model",
		"Spring service dependency injection",
		"form validation",
	})
}

func TestLang_PHP(t *testing.T) {
	runLangSnapshots(t, "php", []string{
		"HTTP request handling",
		"database query builder",
		"authentication guard",
		"collection helper methods",
		"middleware pipeline",
		"model relationships",
	})
}

func TestLang_JavaScript(t *testing.T) {
	runLangSnapshots(t, "js", []string{
		"HTTP router middleware",
		"request response handling",
		"event emitter",
		"file system operations",
		"error handling",
	})
}

func TestLang_TypeScript(t *testing.T) {
	runLangSnapshots(t, "ts", []string{
		"event listener registration",
		"async operation with cancellation",
		"URI path manipulation",
		"lifecycle disposable",
		"platform detection",
		"stream reader writer",
	})
}

func TestLang_Ruby(t *testing.T) {
	runLangSnapshots(t, "ruby", []string{
		"route matching",
		"controller action handling",
		"database record query",
		"authentication callback",
		"template rendering",
	})
}

func TestLang_Python(t *testing.T) {
	runLangSnapshots(t, "python", []string{
		"HTTP route handler",
		"database model query",
		"authentication view",
		"request context",
		"error exception handling",
		"template rendering",
	})
}

func TestLang_Rust(t *testing.T) {
	runLangSnapshots(t, "rust", []string{
		"async runtime executor",
		"file search and match",
		"TCP network listener",
		"mutex lock concurrency",
		"error result handling",
		"command line argument parsing",
	})
}

func TestLang_YAML(t *testing.T) {
	runLangSnapshots(t, "yaml", []string{
		"Kubernetes deployment configuration",
		"CI build steps",
		"service port definition",
		"environment variables",
	})
}

func TestLang_Markdown(t *testing.T) {
	runLangSnapshots(t, "md", []string{
		"installation setup instructions",
		"error handling patterns",
		"ownership and borrowing",
		"authentication guide",
	})
}

func TestLang_JSON(t *testing.T) {
	runLangSnapshots(t, "json", []string{
		"npm build scripts",
		"TypeScript compiler configuration",
		"API endpoint paths",
		"dependency versions",
	})
}
```

> **Note:** The `session` parameter typing above is simplified for plan
> readability. The actual implementation uses `*mcp.ClientSession` from the
> e2e_test.go helpers — use `startServer(t)` which already returns the right
> type. Remove the `mcpSession` type alias and cast in the real code.

**Step 2:** Run to generate initial snapshots:

```bash
UPDATE_SNAPSHOTS=true CGO_ENABLED=1 go test -tags=e2e -run 'TestLang_' -timeout=30m -v ./...
```

Expected: all tests PASS, `.snapshots/` directory populated.

**Step 3:** Review snapshots — look for:

- Results containing only `package` kind → update query or check chunker skips
  package decls
- Unexpectedly empty results → query too specific or chunker not producing
  chunks
- Results with wrong language files mixed in → check extension registration

**Step 4:** If snapshot review reveals issues, fix chunkers/queries and re-run
with `UPDATE_SNAPSHOTS=true`.

**Step 5:** Commit snapshots and test file:

```bash
git add e2e_lang_test.go .snapshots/
git commit -m "test: add multi-language cupaloy snapshot E2E tests"
```

---

## Task 7: Update scoring defaults

**Files:**

- Modify: `cmd/stdio.go` — `SemanticSearchInput.Limit`, `MinScore` description
- Modify: `cmd/search.go` — `--limit` and `--min-score` flag defaults
- Modify: `cmd/stdio.go` — `runSemanticSearch` limit fallback

**Step 1:** In `cmd/search.go`, update flag defaults:

```go
// Change from:
searchCmd.Flags().IntP("limit", "l", 10, "max results to return")
searchCmd.Flags().Float64P("min-score", "s", 0, "minimum score threshold (0-1), omit or 0 for no filtering")

// Change to:
searchCmd.Flags().IntP("limit", "l", 50, "max results to return")
searchCmd.Flags().Float64P("min-score", "s", 0.5, "minimum score threshold; results below this score are excluded (use -1 to return all results)")
```

Also update the `maxDistance` conversion to handle `-1` (no filter) and `0.5`
default:

```go
// Change from:
var maxDistance float64
if minScore > 0 {
    maxDistance = 1.0 - minScore
}

// Change to:
var maxDistance float64
if minScore > -1 {
    maxDistance = 1.0 - minScore
}
```

**Step 2:** In `cmd/stdio.go`, update `SemanticSearchInput`:

```go
// Change jsonschema tags:
Limit    int      `json:"limit,omitempty" jsonschema:"Max results to return, default 50"`
MinScore *float64 `json:"min_score,omitempty" jsonschema:"Minimum score threshold (0 to 1). Results below this score are excluded. Default 0.5. Use -1 to return all results regardless of score."`
```

Update the handler where `Limit` fallback is set (find the `if input.Limit == 0`
block):

```go
if input.Limit == 0 {
    input.Limit = 50
}
```

Update `maxDistance` conversion in the MCP handler:

```go
var maxDistance float64
if input.MinScore != nil && *input.MinScore > -1 {
    maxDistance = 1.0 - *input.MinScore
} else if input.MinScore == nil {
    // Default: 0.5 min_score → 0.5 maxDistance
    maxDistance = 0.5
}
```

**Step 3:** Update existing E2E tests that rely on old defaults — search for
`"limit": 10` or no limit specified and ensure they still pass (they use
`min_score: -1` to bypass filtering).

**Step 4:** Build and run unit tests:

```bash
CGO_ENABLED=1 go build ./... && CGO_ENABLED=1 go test ./...
```

**Step 5:** Commit

```bash
git add cmd/search.go cmd/stdio.go
git commit -m "feat: change default limit to 50, default min_score to 0.5"
```

---

## Task 8: Update README + CI badge

**Files:** `README.md`, `.github/workflows/ci.yml`

**Step 1:** Add CI badges at the top of `README.md` (after the title
`# agent-index`):

```markdown
[![CI](https://github.com/aeneasr/agent-index-go/actions/workflows/ci.yml/badge.svg)](https://github.com/aeneasr/agent-index-go/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
```

**Step 2:** Update the "Go is the primary language" paragraph to mention all
supported languages with chunking strategies. Replace with:

```markdown
Supports **11 language families** with semantic chunking:

| Language         | Extensions                                | Chunking strategy                                                   |
| ---------------- | ----------------------------------------- | ------------------------------------------------------------------- |
| Go               | `.go`                                     | Native Go AST — functions, methods, types, interfaces, consts, vars |
| TypeScript / TSX | `.ts`, `.tsx`                             | tree-sitter — functions, classes, interfaces, type aliases, methods |
| JavaScript / JSX | `.js`, `.jsx`, `.mjs`                     | tree-sitter — functions, classes, methods, generators               |
| Python           | `.py`                                     | tree-sitter — function definitions, class definitions               |
| Rust             | `.rs`                                     | tree-sitter — functions, structs, enums, traits, impls, consts      |
| Ruby             | `.rb`                                     | tree-sitter — methods, singleton methods, classes, modules          |
| Java             | `.java`                                   | tree-sitter — methods, classes, interfaces, constructors, enums     |
| PHP              | `.php`                                    | tree-sitter — functions, classes, interfaces, traits, methods       |
| C / C++          | `.c`, `.h`, `.cpp`, `.cc`, `.cxx`, `.hpp` | tree-sitter — function definitions, structs, enums, classes         |
| Markdown / MDX   | `.md`, `.mdx`                             | Heading-based — each `#` / `##` / `###` section is one chunk        |
| YAML / JSON      | `.yaml`, `.yml`, `.json`                  | Key-based — each top-level key and its value block is one chunk     |
```

**Step 3:** Update the MCP Tools section to reflect new defaults (limit=50,
min_score=0.5):

```markdown
| `limit` | integer | no | 50 | Max results | | `min_score` | float | no | 0.5 |
Minimum score threshold (−1 to 1). Use -1 to return all results. |
```

**Step 4:** Update the `agent-index search` flags table:

```markdown
| `--limit` | `-l` | 50 | Max results to return | | `--min-score` | `-s` | 0.5 |
Minimum score threshold; use -1 to return all results |
```

**Step 5:** Update `ci.yml` to also run snapshot tests (add a note that e2e
includes lang tests): The existing `ci.yml` E2E job already runs
`go test -tags=e2e ./...` which will include `TestLang_*`. No change needed
unless you want a separate job. Consider adding:

```yaml
- name: Update snapshots check
  run: |
    CGO_ENABLED=1 go test -tags=e2e -run 'TestLang_' -timeout=30m -v ./...
```

inside the e2e job (after existing E2E tests step), or just rely on the existing
catch-all.

**Step 6:** Commit

```bash
git add README.md .github/workflows/ci.yml
git commit -m "docs: update README with language table, new defaults, CI badges"
```

---

## Final verification

```bash
# Unit tests
CGO_ENABLED=1 go test ./...

# E2E tests (requires Ollama with all-minilm)
CGO_ENABLED=1 go test -tags=e2e -timeout=30m -v ./...
```

Expected:

- All unit tests PASS
- All E2E tests PASS (including `TestLang_*` snapshot tests)
- No snapshot diffs (if snapshots were committed correctly)
