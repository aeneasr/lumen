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

package index

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/aeneasr/agent-index/internal/chunker"
)

// splitOversizedChunks splits chunks whose estimated token count exceeds
// maxTokens into smaller sub-chunks at line boundaries. Chunks under the
// limit pass through unchanged. Token count is estimated as len(content)/4.
func splitOversizedChunks(chunks []chunker.Chunk, maxTokens int) []chunker.Chunk {
	if maxTokens <= 0 {
		return chunks
	}

	maxChars := maxTokens * 4
	var result []chunker.Chunk
	for _, c := range chunks {
		if len(c.Content) <= maxChars {
			result = append(result, c)
			continue
		}
		subChunks := splitChunk(c, maxChars)
		result = append(result, subChunks...)
	}
	return result
}

func splitChunk(c chunker.Chunk, maxChars int) []chunker.Chunk {
	lines := splitContentByLines(c.Content)
	parts := partitionLines(lines, maxChars)

	if len(parts) <= 1 {
		return []chunker.Chunk{c}
	}

	return createSubChunks(c, parts)
}

func splitContentByLines(content string) []string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func partitionLines(lines []string, maxChars int) [][]string {
	var parts [][]string
	var current []string
	currentLen := 0
	for _, line := range lines {
		if currentLen+len(line) > maxChars && len(current) > 0 {
			parts = append(parts, current)
			current = nil
			currentLen = 0
		}
		current = append(current, line)
		currentLen += len(line)
	}
	if len(current) > 0 {
		parts = append(parts, current)
	}
	return parts
}

func createSubChunks(c chunker.Chunk, parts [][]string) []chunker.Chunk {
	totalParts := len(parts)
	var result []chunker.Chunk
	lineOffset := 0

	for i, part := range parts {
		content := strings.Join(part, "")
		startLine := c.StartLine + lineOffset
		endLine := startLine + len(part) - 1
		symbol := fmt.Sprintf("%s[%d/%d]", c.Symbol, i+1, totalParts)

		h := sha256.New()
		h.Write([]byte(c.FilePath))
		h.Write([]byte{':'})
		h.Write([]byte(content))
		id := fmt.Sprintf("%x", h.Sum(nil))[:16]

		result = append(result, chunker.Chunk{
			ID:        id,
			FilePath:  c.FilePath,
			Symbol:    symbol,
			Kind:      c.Kind,
			StartLine: startLine,
			EndLine:   endLine,
			Content:   content,
		})

		lineOffset += len(part)
	}
	return result
}
