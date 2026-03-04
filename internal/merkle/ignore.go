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

package merkle

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"

	ignore "github.com/sabhiram/go-gitignore"
)

// SkipDirs is the canonical set of directory basenames that are always skipped
// during tree building, regardless of .gitignore rules.
var SkipDirs = map[string]bool{
	// VCS
	".git": true, ".hg": true, ".svn": true,
	// Go
	"vendor": true,
	// JS/Node
	"node_modules": true, "bower_components": true, ".next": true, ".nuxt": true,
	// Python
	"__pycache__": true, ".venv": true, "venv": true, ".tox": true, ".eggs": true,
	// Ruby
	".bundle": true,
	// Rust
	"target": true,
	// Java
	".gradle": true,
	// Elixir/Erlang
	"_build": true, "deps": true,
	// General build/cache
	"dist": true, ".cache": true, ".output": true, ".build": true,
	// IDE
	".idea": true, ".vscode": true,
	// Test fixtures (Go convention)
	"testdata": true,
}

// dirIgnore holds compiled matchers for a single directory level.
type dirIgnore struct {
	gitignore     *ignore.GitIgnore // from .gitignore
	lumenIgnore   *ignore.GitIgnore // from .lumenignore
	gitattributes *ignore.GitIgnore // linguist-generated patterns from .gitattributes
}

// IgnoreTree manages hierarchical ignore rules, lazily loading them as
// filepath.WalkDir traverses directories. It is safe for concurrent use.
type IgnoreTree struct {
	rootDir string
	extSet  map[string]bool

	mu   sync.Mutex
	dirs map[string]*dirIgnore // keyed by relative dir path ("" = root)
}

// NewIgnoreTree creates an IgnoreTree rooted at rootDir that filters by the
// given file extensions. Root-level ignore files are loaded eagerly.
func NewIgnoreTree(rootDir string, exts []string) *IgnoreTree {
	extSet := make(map[string]bool, len(exts))
	for _, ext := range exts {
		extSet[ext] = true
	}
	t := &IgnoreTree{
		rootDir: rootDir,
		extSet:  extSet,
		dirs:    make(map[string]*dirIgnore),
	}
	t.loadDir("") // eagerly load root
	return t
}

// loadDir loads ignore files for a directory (relative path). Must be called
// with t.mu held.
func (t *IgnoreTree) loadDir(dirRel string) *dirIgnore {
	if d, ok := t.dirs[dirRel]; ok {
		return d
	}

	absDir := filepath.Join(t.rootDir, dirRel)
	d := &dirIgnore{}

	if gi, err := ignore.CompileIgnoreFile(filepath.Join(absDir, ".gitignore")); err == nil {
		d.gitignore = gi
	}
	if ai, err := ignore.CompileIgnoreFile(filepath.Join(absDir, ".lumenignore")); err == nil {
		d.lumenIgnore = ai
	}
	if ga := parseLinguistGenerated(filepath.Join(absDir, ".gitattributes")); ga != nil {
		d.gitattributes = ga
	}

	t.dirs[dirRel] = d
	return d
}

// shouldSkip implements SkipFunc. It checks the five filtering layers:
// 1. SkipDirs, 2. .gitignore, 3. .lumenignore, 4. .gitattributes, 5. extension.
func (t *IgnoreTree) shouldSkip(relPath string, isDir bool) bool {
	if isDir && SkipDirs[filepath.Base(relPath)] {
		return true
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	parentDir := filepath.Dir(relPath)
	ancestors := ancestorDirs(parentDir)
	for _, anc := range ancestors {
		if t.checkIgnoreRules(relPath, anc, isDir) {
			return true
		}
	}

	return !isDir && !t.extSet[filepath.Ext(relPath)]
}

func (t *IgnoreTree) checkIgnoreRules(relPath, anc string, isDir bool) bool {
	d := t.loadDir(anc)
	pathFromAnc := getPathFromAncestor(relPath, anc)
	matchPath := pathFromAnc
	if isDir {
		matchPath = pathFromAnc + "/"
	}

	if d.gitignore != nil && d.gitignore.MatchesPath(matchPath) {
		return true
	}
	if d.lumenIgnore != nil && d.lumenIgnore.MatchesPath(matchPath) {
		return true
	}
	if !isDir && d.gitattributes != nil && d.gitattributes.MatchesPath(pathFromAnc) {
		return true
	}
	return false
}

func getPathFromAncestor(relPath, anc string) string {
	if anc == "" {
		return relPath
	}
	pathFromAnc, _ := filepath.Rel(anc, relPath)
	return pathFromAnc
}

// ancestorDirs returns the directory hierarchy from root ("") to dirRel.
// For "a/b/c" it returns ["", "a", "a/b", "a/b/c"].
// For "." or "" it returns [""].
func ancestorDirs(dirRel string) []string {
	if dirRel == "." || dirRel == "" {
		return []string{""}
	}
	parts := strings.Split(filepath.ToSlash(dirRel), "/")
	result := make([]string, 0, len(parts)+1)
	result = append(result, "")
	for i := range parts {
		result = append(result, filepath.Join(parts[:i+1]...))
	}
	return result
}

// parseLinguistGenerated reads a .gitattributes file and returns a compiled
// matcher for patterns marked with linguist-generated or linguist-generated=true.
// Returns nil if the file doesn't exist or contains no such patterns.
func parseLinguistGenerated(path string) *ignore.GitIgnore {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}

		// Split into pattern and attributes
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pattern := fields[0]
		for _, attr := range fields[1:] {
			// Match "linguist-generated" (bare) or "linguist-generated=true"
			// but NOT "linguist-generated=false" or "-linguist-generated"
			if attr == "linguist-generated" || attr == "linguist-generated=true" {
				patterns = append(patterns, pattern)
				break
			}
		}
	}

	if len(patterns) == 0 {
		return nil
	}
	return ignore.CompileIgnoreLines(patterns...)
}

// MakeSkip returns a SkipFunc that layers five filters:
//  1. SkipDirs — map lookup on directory basename (cheapest check)
//  2. .gitignore — root + nested, hierarchical matching
//  3. .lumenignore — root + nested, hierarchical matching
//  4. .gitattributes — linguist-generated patterns, root + nested
//  5. Extension filter — only index files whose extension is in exts
//
// Ignore files are discovered lazily as the walk proceeds.
func MakeSkip(rootDir string, exts []string) SkipFunc {
	tree := NewIgnoreTree(rootDir, exts)
	return tree.shouldSkip
}
