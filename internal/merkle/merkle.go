// Package merkle provides file-tree hashing for change detection.
package merkle

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Tree holds the Merkle tree state for a project directory.
type Tree struct {
	RootHash string            // SHA-256 of the root directory
	Files    map[string]string // relative path -> content SHA-256 hash
}

// SkipFunc returns true for paths that should be skipped during tree building.
type SkipFunc func(relPath string, isDir bool) bool

// DefaultSkip skips directories in SkipDirs and non-.go files.
func DefaultSkip(relPath string, isDir bool) bool {
	base := filepath.Base(relPath)
	if isDir {
		return SkipDirs[base]
	}
	return !strings.HasSuffix(base, ".go")
}

// MakeExtSkip returns a SkipFunc that passes only files whose extension is in exts.
// Directories in SkipDirs are always skipped.
func MakeExtSkip(exts []string) SkipFunc {
	extSet := make(map[string]bool, len(exts))
	for _, ext := range exts {
		extSet[ext] = true
	}
	return func(relPath string, isDir bool) bool {
		if isDir {
			return SkipDirs[filepath.Base(relPath)]
		}
		return !extSet[filepath.Ext(relPath)]
	}
}

const merkleWorkers = 8

// BuildTree walks rootDir and computes a Merkle tree.
// File reads are parallelized across up to merkleWorkers goroutines.
// If skip is nil, DefaultSkip is used.
func BuildTree(rootDir string, skip SkipFunc) (*Tree, error) {
	if skip == nil {
		skip = DefaultSkip
	}

	// Phase 1: collect file paths (sequential walk, cheap).
	var relPaths []string
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
		if !skip(rel, false) {
			relPaths = append(relPaths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Phase 2: hash files concurrently with a bounded worker pool.
	type result struct {
		rel  string
		hash string
		err  error
	}

	work := make(chan string, len(relPaths))
	for _, p := range relPaths {
		work <- p
	}
	close(work)

	results := make(chan result, len(relPaths))
	workers := min(merkleWorkers, len(relPaths))
	if workers == 0 {
		workers = 1
	}

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for rel := range work {
				data, err := os.ReadFile(filepath.Join(rootDir, rel))
				if err != nil {
					results <- result{err: err}
					return
				}
				hash := fmt.Sprintf("%x", sha256.Sum256(data))
				results <- result{rel: rel, hash: hash}
			}
		})
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	tree := &Tree{
		Files: make(map[string]string, len(relPaths)),
	}
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		tree.Files[r.rel] = r.hash
	}

	tree.RootHash = buildDirHash(tree.Files)
	return tree, nil
}

func buildDirHash(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)

	h := sha256.New()
	for _, p := range paths {
		_, _ = fmt.Fprintf(h, "%s:%s\n", p, files[p])
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
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(modified)
	return
}
