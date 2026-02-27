package merkle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakeSkip_GitignorePatterns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "*.log\nbuild/\n")

	skip := MakeSkip(dir, []string{".go", ".log"})

	// .log files should be skipped by gitignore even though extension is allowed
	if !skip("app.log", false) {
		t.Error("expected app.log to be skipped via .gitignore")
	}
	// build/ directory should be skipped by gitignore
	if !skip("build", true) {
		t.Error("expected build/ to be skipped via .gitignore")
	}
	// .go files should pass
	if skip("main.go", false) {
		t.Error("expected main.go to pass")
	}
	// .txt files should be skipped by extension filter (not in exts)
	if !skip("readme.txt", false) {
		t.Error("expected readme.txt to be skipped by extension filter")
	}
}

func TestMakeSkip_NoGitignore(t *testing.T) {
	dir := t.TempDir()
	// No .gitignore created

	skip := MakeSkip(dir, []string{".go", ".py"})

	if skip("main.go", false) {
		t.Error("expected main.go to pass without .gitignore")
	}
	if skip("script.py", false) {
		t.Error("expected script.py to pass without .gitignore")
	}
	if !skip("readme.md", false) {
		t.Error("expected readme.md to be skipped by extension filter")
	}
	// Hardcoded dirs still skipped
	if !skip("node_modules", true) {
		t.Error("expected node_modules to be skipped")
	}
}

func TestMakeSkip_NegationPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "*.gen.go\n!important.gen.go\n")

	skip := MakeSkip(dir, []string{".go"})

	if !skip("foo.gen.go", false) {
		t.Error("expected foo.gen.go to be skipped via .gitignore")
	}
	if skip("important.gen.go", false) {
		t.Error("expected important.gen.go to pass via negation pattern")
	}
	if skip("main.go", false) {
		t.Error("expected main.go to pass")
	}
}

func TestMakeSkip_HardcodedDirs(t *testing.T) {
	dir := t.TempDir()
	skip := MakeSkip(dir, []string{".go"})

	for name := range SkipDirs {
		if !skip(name, true) {
			t.Errorf("expected hardcoded dir %q to be skipped", name)
		}
	}

	// Non-skipped dirs should pass
	if skip("src", true) {
		t.Error("expected src/ to pass")
	}
	if skip("pkg", true) {
		t.Error("expected pkg/ to pass")
	}
}

func TestBuildTree_WithGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "generated/\n*.tmp\n")
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "util.go", "package main\n")

	if err := os.MkdirAll(filepath.Join(dir, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "generated/code.go", "package generated\n")
	writeFile(t, dir, "data.tmp", "temp data")

	skip := MakeSkip(dir, []string{".go", ".tmp"})
	tree, err := BuildTree(dir, skip)
	if err != nil {
		t.Fatal(err)
	}

	// Should only have main.go and util.go
	if len(tree.Files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(tree.Files), tree.Files)
	}
	if _, ok := tree.Files["main.go"]; !ok {
		t.Error("expected main.go in tree")
	}
	if _, ok := tree.Files["util.go"]; !ok {
		t.Error("expected util.go in tree")
	}
	if _, ok := tree.Files["generated/code.go"]; ok {
		t.Error("expected generated/code.go to be excluded")
	}
	if _, ok := tree.Files["data.tmp"]; ok {
		t.Error("expected data.tmp to be excluded")
	}
}
