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
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// StructuredChunker splits YAML and JSON files by their key hierarchy.
// It recursively descends the document tree: if a subtree fits within the
// token budget, it is emitted as one chunk with its dotted key path prepended.
// If a subtree is too large, the chunker recurses into its children.
// The splitOversizedChunks pipeline in index/split.go is the backstop for
// leaf nodes that cannot be subdivided further.
type StructuredChunker struct {
	maxChars int // maxTokens * 4 (1 token ≈ 4 chars)
}

// NewStructuredChunker returns a StructuredChunker. maxTokens is the token
// budget per chunk; use LUMEN_MAX_CHUNK_TOKENS (default 2048).
func NewStructuredChunker(maxTokens int) *StructuredChunker {
	return &StructuredChunker{maxChars: maxTokens * 4}
}

// Chunk implements Chunker for YAML and JSON files.
func (c *StructuredChunker) Chunk(filePath string, content []byte) ([]Chunk, error) {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return nil, nil
	}

	// Fast path: small file fits as a single chunk.
	if len(trimmed) <= c.maxChars {
		lines := strings.Count(trimmed, "\n") + 1
		return []Chunk{makeChunk(filePath, "root", "document", 1, lines, trimmed)}, nil
	}

	// Parse into a yaml.Node tree. yaml.v3 understands both YAML and JSON.
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var chunks []Chunk
	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			break // EOF or parse error: return what we have
		}
		if doc.Kind == 0 || len(doc.Content) == 0 {
			continue
		}
		// doc is always a DocumentNode; doc.Content[0] is the root.
		root := doc.Content[0]
		chunks = append(chunks, c.recurse(filePath, root, "")...)
	}

	// If parsing produced nothing (e.g. parse error on large file), fall back.
	if len(chunks) == 0 {
		lines := strings.Count(trimmed, "\n") + 1
		return []Chunk{makeChunk(filePath, "root", "document", 1, lines, trimmed)}, nil
	}
	return chunks, nil
}

// recurse emits chunks for the given node. If the node serializes within
// maxChars, it emits a single chunk. Otherwise it recurses into children.
func (c *StructuredChunker) recurse(filePath string, node *yaml.Node, path string) []Chunk {
	text := serializeNode(node)
	symbol := normalizeSymbol(path)

	if len(text) <= c.maxChars {
		return c.createNodeChunk(filePath, symbol, text, node)
	}

	switch node.Kind {
	case yaml.MappingNode:
		return c.recurseMapping(filePath, node, path)
	case yaml.SequenceNode:
		return c.recurseSequence(filePath, node, path)
	default:
		return c.createNodeChunk(filePath, symbol, text, node)
	}
}

func normalizeSymbol(path string) string {
	if path == "" {
		return "root"
	}
	return path
}

func (c *StructuredChunker) createNodeChunk(filePath, symbol, text string, node *yaml.Node) []Chunk {
	content := "# path: " + symbol + "\n" + text
	startLine := node.Line
	if startLine == 0 {
		startLine = 1
	}
	endLine := startLine + strings.Count(text, "\n")
	return []Chunk{makeChunk(filePath, symbol, "section", startLine, endLine, content)}
}

func (c *StructuredChunker) recurseMapping(filePath string, node *yaml.Node, path string) []Chunk {
	var chunks []Chunk
	for i := 0; i+1 < len(node.Content); i += 2 {
		chunks = append(chunks, c.processMappingPair(filePath, node, path, i)...)
	}
	return chunks
}

func (c *StructuredChunker) processMappingPair(filePath string, node *yaml.Node, path string, i int) []Chunk {
	keyNode := node.Content[i]
	valNode := node.Content[i+1]
	childPath := joinKeyPath(path, keyNode.Value)
	wrapper := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{keyNode, valNode}}
	wrapText := serializeNode(wrapper)

	if len(wrapText) <= c.maxChars {
		return c.createNodeChunk(filePath, childPath, wrapText, keyNode)
	}
	return c.recurse(filePath, valNode, childPath)
}

func (c *StructuredChunker) recurseSequence(filePath string, node *yaml.Node, path string) []Chunk {
	var chunks []Chunk
	for i, item := range node.Content {
		childPath := fmt.Sprintf("%s[%d]", path, i)
		chunks = append(chunks, c.recurse(filePath, item, childPath)...)
	}
	return chunks
}

// serializeNode marshals a yaml.Node to text. Returns empty string on error.
func serializeNode(node *yaml.Node) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}

// joinKeyPath builds "parent.child"; if parent is empty returns child.
func joinKeyPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}
