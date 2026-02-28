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

package chunker

import (
	"github.com/aeneasr/agent-index/internal/config"

	sitter_c   "github.com/smacker/go-tree-sitter/c"
	sitter_cpp "github.com/smacker/go-tree-sitter/cpp"
	sitter_java "github.com/smacker/go-tree-sitter/java"
	sitter_js   "github.com/smacker/go-tree-sitter/javascript"
	sitter_php  "github.com/smacker/go-tree-sitter/php"
	sitter_py   "github.com/smacker/go-tree-sitter/python"
	sitter_rb   "github.com/smacker/go-tree-sitter/ruby"
	sitter_rs   "github.com/smacker/go-tree-sitter/rust"
	sitter_ts   "github.com/smacker/go-tree-sitter/typescript/typescript"
	sitter_tsx  "github.com/smacker/go-tree-sitter/typescript/tsx"
)

// supportedExtensions is the canonical list of file extensions handled by DefaultLanguages.
var supportedExtensions = []string{
	".go",
	".ts", ".tsx",
	".js", ".jsx", ".mjs",
	".py",
	".rs",
	".rb",
	".java",
	".c", ".h",
	".cpp", ".cc", ".cxx", ".hpp",
	".php",
	".md", ".mdx",
	".yaml", ".yml", ".json",
}

// SupportedExtensions returns the file extensions indexed by DefaultLanguages.
func SupportedExtensions() []string { return supportedExtensions }

// DefaultLanguages returns a map of file extension → Chunker for all supported languages.
// Panics if any hardcoded query pattern is invalid (programming error).
func DefaultLanguages() map[string]Chunker {
	py := mustTreeSitterChunker(LanguageDef{
		Language: sitter_py.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_definition name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_definition name: (identifier) @name) @decl`, Kind: "type"},
		},
	})

	ts := mustTreeSitterChunker(LanguageDef{
		Language: sitter_ts.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(interface_declaration name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(type_alias_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(method_definition name: (property_identifier) @name) @decl`, Kind: "method"},
		},
	})

	tsx := mustTreeSitterChunker(LanguageDef{
		Language: sitter_tsx.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(interface_declaration name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(type_alias_declaration name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(method_definition name: (property_identifier) @name) @decl`, Kind: "method"},
		},
	})

	js := mustTreeSitterChunker(LanguageDef{
		Language: sitter_js.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class_declaration name: (identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(method_definition name: (property_identifier) @name) @decl`, Kind: "method"},
			{Pattern: `(generator_function_declaration name: (identifier) @name) @decl`, Kind: "function"},
		},
	})

	rs := mustTreeSitterChunker(LanguageDef{
		Language: sitter_rs.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_item name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(struct_item name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(enum_item name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(trait_item name: (type_identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(const_item name: (identifier) @name) @decl`, Kind: "const"},
		},
	})

	rb := mustTreeSitterChunker(LanguageDef{
		Language: sitter_rb.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(method name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(singleton_method name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(class name: (constant) @name) @decl`, Kind: "type"},
			{Pattern: `(module name: (constant) @name) @decl`, Kind: "type"},
		},
	})

	java := mustTreeSitterChunker(LanguageDef{
		Language: sitter_java.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(method_declaration name: (identifier) @name) @decl`, Kind: "method"},
			{Pattern: `(class_declaration name: (identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(interface_declaration name: (identifier) @name) @decl`, Kind: "interface"},
			{Pattern: `(constructor_declaration name: (identifier) @name) @decl`, Kind: "function"},
			{Pattern: `(enum_declaration name: (identifier) @name) @decl`, Kind: "type"},
		},
	})

	c := mustTreeSitterChunker(LanguageDef{
		Language: sitter_c.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_definition declarator: (function_declarator declarator: (identifier) @name)) @decl`, Kind: "function"},
			{Pattern: `(function_definition declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))) @decl`, Kind: "function"},
			{Pattern: `(struct_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(enum_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
		},
	})

	cpp := mustTreeSitterChunker(LanguageDef{
		Language: sitter_cpp.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_definition declarator: (function_declarator declarator: (identifier) @name)) @decl`, Kind: "function"},
			{Pattern: `(function_definition declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))) @decl`, Kind: "function"},
			{Pattern: `(class_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(struct_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
			{Pattern: `(enum_specifier name: (type_identifier) @name) @decl`, Kind: "type"},
		},
	})

	php := mustTreeSitterChunker(LanguageDef{
		Language: sitter_php.GetLanguage(),
		Queries: []QueryDef{
			{Pattern: `(function_definition name: (name) @name) @decl`, Kind: "function"},
			{Pattern: `(interface_declaration name: (name) @name) @decl`, Kind: "interface"},
			{Pattern: `(trait_declaration name: (name) @name) @decl`, Kind: "type"},
			{Pattern: `(method_declaration name: (name) @name) @decl`, Kind: "method"},
		},
	})

	goChunker := NewGoAST()

	md := NewMarkdownChunker()

	maxChunkTokens := config.EnvOrDefaultInt("AGENT_INDEX_MAX_CHUNK_TOKENS", 2048)
	structured := NewStructuredChunker(maxChunkTokens)

	return map[string]Chunker{
		".go":   goChunker,
		".ts":   ts,
		".tsx":  tsx,
		".js":   js,
		".jsx":  js,
		".mjs":  js,
		".py":   py,
		".rs":   rs,
		".rb":   rb,
		".java": java,
		".c":    c,
		".h":    c,
		".cpp":  cpp,
		".cc":   cpp,
		".cxx":  cpp,
		".hpp":  cpp,
		".php":  php,
		".md":   md,
		".mdx":  md,
		".yaml": structured,
		".yml":  structured,
		".json": structured,
	}
}
