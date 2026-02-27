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
	Files    map[string]string // relative path -> content SHA-256 hash
	Dirs     map[string]string // relative dir path -> directory hash
}

// SkipFunc returns true for paths that should be skipped during tree building.
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

	tree.RootHash = buildDirHash(tree.Files)
	return tree, nil
}

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
