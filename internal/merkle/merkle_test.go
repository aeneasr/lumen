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
