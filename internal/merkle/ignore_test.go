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

func TestMakeSkip_NestedGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, "sub/.gitignore", "secret.go\n")
	writeFile(t, dir, "sub/main.go", "package sub\n")
	writeFile(t, dir, "sub/secret.go", "package sub\n")

	skip := MakeSkip(dir, []string{".go", ".log"})

	// Root .gitignore applies everywhere
	if !skip("app.log", false) {
		t.Error("expected app.log to be skipped via root .gitignore")
	}
	if !skip("sub/app.log", false) {
		t.Error("expected sub/app.log to be skipped via root .gitignore")
	}
	// Nested .gitignore applies in its directory
	if !skip("sub/secret.go", false) {
		t.Error("expected sub/secret.go to be skipped via nested .gitignore")
	}
	// Nested .gitignore does not affect root
	if skip("secret.go", false) {
		t.Error("expected root secret.go to pass (nested .gitignore doesn't apply at root)")
	}
	// Normal file in sub passes
	if skip("sub/main.go", false) {
		t.Error("expected sub/main.go to pass")
	}
}

func TestMakeSkip_LumenIgnore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".lumenignore", "generated/\n*.pb.go\n")

	skip := MakeSkip(dir, []string{".go"})

	if !skip("generated", true) {
		t.Error("expected generated/ to be skipped via .lumenignore")
	}
	if !skip("foo.pb.go", false) {
		t.Error("expected foo.pb.go to be skipped via .lumenignore")
	}
	if skip("main.go", false) {
		t.Error("expected main.go to pass")
	}
}

func TestMakeSkip_NestedLumenIgnore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "api/.lumenignore", "mock_*.go\n")
	writeFile(t, dir, "api/handler.go", "package api\n")
	writeFile(t, dir, "api/mock_handler.go", "package api\n")

	skip := MakeSkip(dir, []string{".go"})

	if !skip("api/mock_handler.go", false) {
		t.Error("expected api/mock_handler.go to be skipped via nested .lumenignore")
	}
	if skip("api/handler.go", false) {
		t.Error("expected api/handler.go to pass")
	}
	// Root level mock_ files are not affected
	if skip("mock_handler.go", false) {
		t.Error("expected root mock_handler.go to pass (nested ignore doesn't apply)")
	}
}

func TestMakeSkip_GitattributesGenerated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitattributes", "bench-results linguist-generated\ndocs/plans linguist-generated=true\n")

	skip := MakeSkip(dir, []string{".go", ".txt"})

	if !skip("bench-results/data.txt", false) {
		t.Error("expected bench-results/data.txt to be skipped via linguist-generated")
	}
	if !skip("docs/plans/plan.txt", false) {
		t.Error("expected docs/plans/plan.txt to be skipped via linguist-generated=true")
	}
	if skip("src/main.go", false) {
		t.Error("expected src/main.go to pass")
	}
}

func TestMakeSkip_GitattributesNestedDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pkg/.gitattributes", "generated_*.go linguist-generated\n")

	skip := MakeSkip(dir, []string{".go"})

	if !skip("pkg/generated_types.go", false) {
		t.Error("expected pkg/generated_types.go to be skipped via nested .gitattributes")
	}
	if skip("pkg/types.go", false) {
		t.Error("expected pkg/types.go to pass")
	}
	// Root level is not affected
	if skip("generated_types.go", false) {
		t.Error("expected root generated_types.go to pass")
	}
}

func TestMakeSkip_GitattributesNonGenerated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitattributes", "*.go linguist-language=Go\nvendored/* linguist-vendored\nkeep.go linguist-generated=false\n")

	skip := MakeSkip(dir, []string{".go"})

	// Non-generated attributes should not cause skipping
	if skip("main.go", false) {
		t.Error("expected main.go to pass (linguist-language is not linguist-generated)")
	}
	if skip("keep.go", false) {
		t.Error("expected keep.go to pass (linguist-generated=false)")
	}
}

func TestMakeSkip_AllLayersCombined(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, ".lumenignore", "scratch/\n")
	writeFile(t, dir, ".gitattributes", "generated.go linguist-generated\n")
	writeFile(t, dir, "sub/.gitignore", "tmp_*.go\n")

	skip := MakeSkip(dir, []string{".go", ".log"})

	// Layer 1: SkipDirs
	if !skip("node_modules", true) {
		t.Error("expected node_modules to be skipped (SkipDirs)")
	}
	// Layer 2: .gitignore (root)
	if !skip("debug.log", false) {
		t.Error("expected debug.log to be skipped (.gitignore)")
	}
	// Layer 2: .gitignore (nested)
	if !skip("sub/tmp_data.go", false) {
		t.Error("expected sub/tmp_data.go to be skipped (nested .gitignore)")
	}
	// Layer 3: .lumenignore
	if !skip("scratch", true) {
		t.Error("expected scratch/ to be skipped (.lumenignore)")
	}
	// Layer 4: .gitattributes
	if !skip("generated.go", false) {
		t.Error("expected generated.go to be skipped (.gitattributes)")
	}
	// Layer 5: extension filter
	if !skip("readme.md", false) {
		t.Error("expected readme.md to be skipped (extension filter)")
	}
	// Normal file passes all layers
	if skip("main.go", false) {
		t.Error("expected main.go to pass all layers")
	}
}

func TestParseLinguistGenerated(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content string
		match   []string // paths that should match
		noMatch []string // paths that should not match
	}{
		{
			name:    "bare linguist-generated",
			content: "bench-results linguist-generated\n",
			match:   []string{"bench-results"},
			noMatch: []string{"other"},
		},
		{
			name:    "linguist-generated=true",
			content: "docs/plans linguist-generated=true\n",
			match:   []string{"docs/plans"},
			noMatch: []string{"docs/other"},
		},
		{
			name:    "linguist-generated=false ignored",
			content: "keep.go linguist-generated=false\n",
			noMatch: []string{"keep.go"},
		},
		{
			name:    "negated -linguist-generated ignored",
			content: "keep.go -linguist-generated\n",
			noMatch: []string{"keep.go"},
		},
		{
			name:    "multiple attributes",
			content: "*.pb.go linguist-generated text\n",
			match:   []string{"foo.pb.go"},
			noMatch: []string{"foo.go"},
		},
		{
			name:    "comments and blanks",
			content: "# comment\n\n*.gen.go linguist-generated\n",
			match:   []string{"foo.gen.go"},
			noMatch: []string{"foo.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".gitattributes")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			gi := parseLinguistGenerated(path)

			for _, p := range tt.match {
				if gi == nil || !gi.MatchesPath(p) {
					t.Errorf("expected %q to match", p)
				}
			}
			for _, p := range tt.noMatch {
				if gi != nil && gi.MatchesPath(p) {
					t.Errorf("expected %q to NOT match", p)
				}
			}
		})
	}

	// Non-existent file
	if gi := parseLinguistGenerated(filepath.Join(dir, "nonexistent")); gi != nil {
		t.Error("expected nil for non-existent file")
	}
}

func TestAncestorDirs(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", []string{""}},
		{".", []string{""}},
		{"a", []string{"", "a"}},
		{"a/b", []string{"", "a", "a/b"}},
		{"a/b/c", []string{"", "a", "a/b", "a/b/c"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ancestorDirs(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("ancestorDirs(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ancestorDirs(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
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

func TestBuildTree_WithNestedGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "sub/sub.go", "package sub\n")
	writeFile(t, dir, "sub/.gitignore", "internal_*.go\n")
	writeFile(t, dir, "sub/internal_helper.go", "package sub\n")
	writeFile(t, dir, "sub/helper.go", "package sub\n")
	writeFile(t, dir, "app.log", "log data")

	skip := MakeSkip(dir, []string{".go", ".log"})
	tree, err := BuildTree(dir, skip)
	if err != nil {
		t.Fatal(err)
	}

	// main.go, sub/sub.go, sub/helper.go should be present
	// app.log (root .gitignore), sub/internal_helper.go (nested .gitignore) excluded
	expected := map[string]bool{
		"main.go":       true,
		"sub/sub.go":    true,
		"sub/helper.go": true,
	}
	if len(tree.Files) != len(expected) {
		t.Fatalf("expected %d files, got %d: %v", len(expected), len(tree.Files), tree.Files)
	}
	for f := range expected {
		if _, ok := tree.Files[f]; !ok {
			t.Errorf("expected %s in tree", f)
		}
	}
	if _, ok := tree.Files["app.log"]; ok {
		t.Error("expected app.log to be excluded by root .gitignore")
	}
	if _, ok := tree.Files["sub/internal_helper.go"]; ok {
		t.Error("expected sub/internal_helper.go to be excluded by nested .gitignore")
	}
}
