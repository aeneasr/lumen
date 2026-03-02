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

package chunker_test

import (
	"strings"
	"testing"

	sitter_py "github.com/smacker/go-tree-sitter/python"
	sitter_rs "github.com/smacker/go-tree-sitter/rust"
	sitter_ts "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/aeneasr/agent-index/internal/chunker"
)

var samplePython = []byte(`def greet(name):
    """Say hello."""
    return f"Hello, {name}"

class Animal:
    def speak(self):
        pass
`)

func TestTreeSitterChunker_Python(t *testing.T) {
	def := chunker.LanguageDef{
		Language: sitter_py.GetLanguage(),
		Queries: []chunker.QueryDef{
			{Pattern: `(function_definition name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_definition name: (identifier) @name) @decl`, Kind: "type"},
		},
	}
	c, err := chunker.NewTreeSitterChunker(def)
	if err != nil {
		t.Fatalf("NewTreeSitterChunker: %v", err)
	}

	chunks, err := c.Chunk("sample.py", samplePython)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	checkChunk(t, bySymbol, "greet", "function", 1, 3, "sample.py", "def greet")
	checkChunk(t, bySymbol, "Animal", "type", 5, 7, "sample.py", "")
	checkChunk(t, bySymbol, "speak", "function", 0, 0, "sample.py", "")
}

func checkChunk(t *testing.T, bySymbol map[string]chunker.Chunk, symbol, kind string, startLine, endLine int, filePath, contentContains string) {
	t.Helper()
	ch, ok := bySymbol[symbol]
	if !ok {
		t.Fatalf("expected chunk %q, got symbols: %v", symbol, getChunkSymbols(bySymbol))
	}
	checkChunkKind(t, symbol, ch.Kind, kind)
	checkChunkLines(t, symbol, ch.StartLine, ch.EndLine, startLine, endLine)
	checkChunkPath(t, symbol, ch.FilePath, filePath)
	checkChunkID(t, symbol, ch.ID)
	checkChunkContent(t, symbol, ch.Content, contentContains)
}

func checkChunkKind(t *testing.T, symbol, actual, expected string) {
	if actual != expected {
		t.Errorf("%s.Kind = %q, want %q", symbol, actual, expected)
	}
}

func checkChunkLines(t *testing.T, symbol string, actualStart, actualEnd, expectedStart, expectedEnd int) {
	if expectedStart > 0 && actualStart != expectedStart {
		t.Errorf("%s.StartLine = %d, want %d", symbol, actualStart, expectedStart)
	}
	if expectedEnd > 0 && actualEnd != expectedEnd {
		t.Errorf("%s.EndLine = %d, want %d", symbol, actualEnd, expectedEnd)
	}
}

func checkChunkPath(t *testing.T, symbol, actual, expected string) {
	if expected != "" && actual != expected {
		t.Errorf("%s.FilePath = %q, want %q", symbol, actual, expected)
	}
}

func checkChunkID(t *testing.T, symbol, id string) {
	if id == "" {
		t.Errorf("%s.ID is empty", symbol)
	}
}

func checkChunkContent(t *testing.T, symbol, content, contains string) {
	if contains != "" && !strings.Contains(content, contains) {
		t.Errorf("%s.Content does not contain %q", symbol, contains)
	}
}

func getChunkSymbols(bySymbol map[string]chunker.Chunk) []string {
	symbols := make([]string, 0, len(bySymbol))
	for s := range bySymbol {
		symbols = append(symbols, s)
	}
	return symbols
}

var sampleTypeScript = []byte(`export function add(a: number, b: number): number {
  return a + b;
}

export class Calculator {
  multiply(x: number, y: number): number {
    return x * y;
  }
}

export abstract class Base {
  abstract doWork(): void;
}

export interface Shape {
  area(): number;
}

export type Color = "red" | "green" | "blue";
`)

func TestTreeSitterChunker_TypeScript(t *testing.T) {
	def := chunker.LanguageDef{
		Language: sitter_ts.GetLanguage(),
		Queries: []chunker.QueryDef{
			{Pattern: `(function_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(abstract_class_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(interface_declaration name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(type_alias_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(method_definition name: (property_identifier) @name) @decl`, Kind: "method"},
		},
	}
	c, err := chunker.NewTreeSitterChunker(def)
	if err != nil {
		t.Fatalf("NewTreeSitterChunker: %v", err)
	}

	chunks, err := c.Chunk("sample.ts", sampleTypeScript)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("add", "function")
	check("Calculator", "type")
	check("Base", "type")
	check("multiply", "method")
	check("Shape", "interface")
	check("Color", "type")
}

var sampleRust = []byte(`pub fn add(a: i32, b: i32) -> i32 {
    a + b
}

pub struct Point {
    pub x: f64,
    pub y: f64,
}

pub trait Drawable {
    fn draw(&self);
}

pub const MAX_SIZE: usize = 1024;
`)

func TestTreeSitterChunker_Rust(t *testing.T) {
	def := chunker.LanguageDef{
		Language: sitter_rs.GetLanguage(),
		Queries: []chunker.QueryDef{
			{Pattern: `(function_item name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(struct_item name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(enum_item name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(trait_item name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(const_item name: (identifier) @name) @decl`, Kind: "const"},
		},
	}
	c, err := chunker.NewTreeSitterChunker(def)
	if err != nil {
		t.Fatalf("NewTreeSitterChunker: %v", err)
	}

	chunks, err := c.Chunk("sample.rs", sampleRust)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("add", "function")
	check("Point", "type")
	check("Drawable", "interface")
	check("MAX_SIZE", "const")
}

var sampleJavaScript = []byte(`function greet(name) {
  return "hello " + name;
}

class Animal {
  speak() {
    return "...";
  }
}
`)

func TestTreeSitterChunker_JavaScript(t *testing.T) {
	langs := chunker.DefaultLanguages(512)
	c, ok := langs[".js"]
	if !ok {
		t.Fatal("DefaultLanguages() missing .js")
	}

	chunks, err := c.Chunk("sample.js", sampleJavaScript)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("greet", "function")
	check("Animal", "type")
	check("speak", "method")
}

var sampleTSX = []byte(`export function render(): JSX.Element {
  return <div />;
}

export class App {
  render(): JSX.Element {
    return <div />;
  }
}
`)

func TestTreeSitterChunker_TSX(t *testing.T) {
	langs := chunker.DefaultLanguages(512)
	c, ok := langs[".tsx"]
	if !ok {
		t.Fatal("DefaultLanguages() missing .tsx")
	}

	chunks, err := c.Chunk("sample.tsx", sampleTSX)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	// There will be two "render" chunks: one function and one method.
	// Use a multi-map to track all chunks by symbol.
	bySymbolKind := make(map[string]map[string]bool) // symbol -> set of kinds
	for _, ch := range chunks {
		if bySymbolKind[ch.Symbol] == nil {
			bySymbolKind[ch.Symbol] = make(map[string]bool)
		}
		bySymbolKind[ch.Symbol][ch.Kind] = true
	}

	if !bySymbolKind["render"]["function"] {
		t.Errorf("missing chunk render/function (got symbols: %v)", symbolNames(chunks))
	}
	if !bySymbolKind["render"]["method"] {
		t.Errorf("missing chunk render/method (got symbols: %v)", symbolNames(chunks))
	}
	if !bySymbolKind["App"]["type"] {
		t.Errorf("missing chunk App/type (got symbols: %v)", symbolNames(chunks))
	}
}

var sampleRuby = []byte(`def greet(name)
  "hello #{name}"
end

class Animal
  def speak
    "..."
  end
end
`)

func TestTreeSitterChunker_Ruby(t *testing.T) {
	langs := chunker.DefaultLanguages(512)
	c, ok := langs[".rb"]
	if !ok {
		t.Fatal("DefaultLanguages() missing .rb")
	}

	chunks, err := c.Chunk("sample.rb", sampleRuby)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("greet", "function")
	check("Animal", "type")
	check("speak", "function") // Ruby methods map to "function" kind
}

var sampleJava = []byte(`public class Calculator {
    public int add(int a, int b) {
        return a + b;
    }
}
`)

func TestTreeSitterChunker_Java(t *testing.T) {
	langs := chunker.DefaultLanguages(512)
	c, ok := langs[".java"]
	if !ok {
		t.Fatal("DefaultLanguages() missing .java")
	}

	chunks, err := c.Chunk("sample.java", sampleJava)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("Calculator", "type")
	check("add", "method")
}

var sampleC = []byte(`int add(int a, int b) {
    return a + b;
}

int *get_ptr(void) {
    return 0;
}

struct Point {
    int x;
    int y;
};
`)

func TestTreeSitterChunker_C(t *testing.T) {
	langs := chunker.DefaultLanguages(512)
	c, ok := langs[".c"]
	if !ok {
		t.Fatal("DefaultLanguages() missing .c")
	}

	chunks, err := c.Chunk("sample.c", sampleC)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("add", "function")
	check("get_ptr", "function") // requires pointer-return fix
	check("Point", "type")
}

var sampleCPP = []byte(`class Vec2 {
    float x;
    float y;
};

int add(int a, int b) {
    return a + b;
}
`)

func TestTreeSitterChunker_CPP(t *testing.T) {
	langs := chunker.DefaultLanguages(512)
	c, ok := langs[".cpp"]
	if !ok {
		t.Fatal("DefaultLanguages() missing .cpp")
	}

	chunks, err := c.Chunk("sample.cpp", sampleCPP)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}

	bySymbol := make(map[string]chunker.Chunk)
	for _, ch := range chunks {
		bySymbol[ch.Symbol] = ch
	}

	check := func(symbol, kind string) {
		t.Helper()
		ch, ok := bySymbol[symbol]
		if !ok {
			t.Errorf("missing chunk %q (got: %v)", symbol, symbolNames(chunks))
			return
		}
		if ch.Kind != kind {
			t.Errorf("chunk %q kind = %q, want %q", symbol, ch.Kind, kind)
		}
	}

	check("Vec2", "type")
	check("add", "function")
}

func symbolNames(chunks []chunker.Chunk) []string {
	names := make([]string, len(chunks))
	for i, c := range chunks {
		names[i] = c.Symbol
	}
	return names
}

func TestMultiChunker_Dispatch(t *testing.T) {
	pyChunker := mustPyChunker(t)

	mc := chunker.NewMultiChunker(map[string]chunker.Chunker{
		".py": pyChunker,
	})

	// Known extension — returns chunks
	chunks, err := mc.Chunk("foo.py", samplePython)
	if err != nil {
		t.Fatalf("Chunk(.py): %v", err)
	}
	if len(chunks) == 0 {
		t.Error("expected chunks for .py, got none")
	}

	// Unknown extension — returns nil, nil
	chunks, err = mc.Chunk("foo.xyz", []byte("hello"))
	if err != nil {
		t.Fatalf("Chunk(.xyz): unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected no chunks for .xyz, got %d", len(chunks))
	}
}

func TestDefaultLanguages_AllExtensionsPresent(t *testing.T) {
	// trivialSources maps file extensions to minimal source containing one named declaration.
	trivialSources := map[string][]byte{
		".go":   []byte("package main\nfunc Foo() {}"),
		".ts":   []byte("function foo() {}"),
		".tsx":  []byte("function foo() { return <div/>; }"),
		".js":   []byte("function foo() {}"),
		".jsx":  []byte("function foo() { return <div/>; }"),
		".mjs":  []byte("function foo() {}"),
		".py":   []byte("def foo():\n    pass"),
		".rs":   []byte("fn foo() {}"),
		".rb":   []byte("def foo\nend"),
		".java": []byte("class Foo {}"),
		".c":    []byte("void foo(void) {}"),
		".h":    []byte("void foo(void) {}"),
		".cpp":  []byte("void foo() {}"),
		".cc":   []byte("void foo() {}"),
		".cxx":  []byte("void foo() {}"),
		".hpp":  []byte("void foo() {}"),
		".php":  []byte("<?php\nfunction foo() {}"),
		".md":   []byte("# Foo\nSome content."),
		".mdx":  []byte("# Foo\nSome content."),
		".yaml": []byte("foo: bar\n"),
		".yml":  []byte("foo: bar\n"),
		".json": []byte(`{"foo": "bar"}`),
	}

	langs := chunker.DefaultLanguages(512)

	for _, ext := range chunker.SupportedExtensions() {
		c, ok := langs[ext]
		if !ok {
			t.Errorf("DefaultLanguages() missing extension %q", ext)
			continue
		}
		src, ok := trivialSources[ext]
		if !ok {
			t.Errorf("trivialSources missing fixture for %q", ext)
			continue
		}
		chunks, err := c.Chunk("test"+ext, src)
		if err != nil {
			t.Errorf("Chunk(%q): %v", ext, err)
			continue
		}
		if len(chunks) == 0 {
			t.Errorf("Chunk(%q): expected at least 1 chunk, got 0", ext)
		}
	}
}

// mustPyChunker creates a Python TreeSitterChunker for use in tests.
func mustPyChunker(t *testing.T) *chunker.TreeSitterChunker {
	t.Helper()
	def := chunker.LanguageDef{
		Language: sitter_py.GetLanguage(),
		Queries: []chunker.QueryDef{
			{Pattern: `(function_definition name: (identifier) @name) @decl`, Kind: "function"},
		},
	}
	c, err := chunker.NewTreeSitterChunker(def)
	if err != nil {
		t.Fatalf("NewTreeSitterChunker: %v", err)
	}
	return c
}
