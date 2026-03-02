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
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// GoAST is a Chunker that uses go/ast to split Go source files into
// semantically meaningful chunks (functions, methods, types, interfaces,
// consts, vars, and the package declaration).
type GoAST struct{}

// NewGoAST returns a new GoAST chunker.
func NewGoAST() *GoAST {
	return &GoAST{}
}

// Chunk parses a Go source file and returns one Chunk per top-level
// declaration, including the package doc chunk.
func (g *GoAST) Chunk(filePath string, content []byte) ([]Chunk, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}

	var chunks []Chunk

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			chunks = append(chunks, chunkFuncDecl(fset, filePath, content, d))
		case *ast.GenDecl:
			chunks = append(chunks, chunkGenDecl(fset, filePath, content, d)...)
		}
	}

	return chunks, nil
}

func chunkFuncDecl(fset *token.FileSet, filePath string, content []byte, d *ast.FuncDecl) Chunk {
	kind := "function"
	symbol := d.Name.Name

	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = "method"
		recvType := receiverTypeName(d.Recv.List[0].Type)
		symbol = recvType + "." + d.Name.Name
	}

	start, end := declRange(fset, d.Doc, d.Pos(), d.End())
	return makeChunk(filePath, symbol, kind, start.Line, end.Line,
		sliceContent(content, start.Offset, end.Offset))
}

func chunkGenDecl(fset *token.FileSet, filePath string, content []byte, d *ast.GenDecl) []Chunk {
	var chunks []Chunk

	// Skip import declarations.
	if d.Tok == token.IMPORT {
		return nil
	}

	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			chunks = append(chunks, chunkTypeSpec(fset, filePath, content, d, s))

		case *ast.ValueSpec:
			chunks = append(chunks, chunkValueSpec(fset, filePath, content, d, s))
		}
	}

	return chunks
}

func chunkTypeSpec(fset *token.FileSet, filePath string, content []byte, d *ast.GenDecl, s *ast.TypeSpec) Chunk {
	kind := "type"
	if _, ok := s.Type.(*ast.InterfaceType); ok {
		kind = "interface"
	}
	doc := s.Doc
	if doc == nil {
		doc = d.Doc
	}
	var start, end token.Position
	if len(d.Specs) == 1 {
		start, end = declRange(fset, doc, d.Pos(), d.End())
	} else {
		start, end = declRange(fset, s.Doc, s.Pos(), s.End())
	}
	return makeChunk(filePath, s.Name.Name, kind, start.Line, end.Line,
		sliceContent(content, start.Offset, end.Offset))
}

func chunkValueSpec(fset *token.FileSet, filePath string, content []byte, d *ast.GenDecl, s *ast.ValueSpec) Chunk {
	kind := "var"
	if d.Tok == token.CONST {
		kind = "const"
	}
	doc := s.Doc
	if doc == nil {
		doc = d.Doc
	}
	var start, end token.Position
	if len(d.Specs) == 1 {
		start, end = declRange(fset, doc, d.Pos(), d.End())
	} else {
		start, end = declRange(fset, s.Doc, s.Pos(), s.End())
	}
	return makeChunk(filePath, s.Names[0].Name, kind, start.Line, end.Line,
		sliceContent(content, start.Offset, end.Offset))
}

func declRange(fset *token.FileSet, doc *ast.CommentGroup, pos, end token.Pos) (token.Position, token.Position) {
	startPos := fset.Position(pos)
	if doc != nil {
		startPos = fset.Position(doc.Pos())
	}
	endPos := fset.Position(end)
	return startPos, endPos
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return "unknown"
}

func sliceContent(content []byte, startOffset, endOffset int) string {
	if startOffset < 0 {
		startOffset = 0
	}
	if endOffset > len(content) {
		endOffset = len(content)
	}
	return string(content[startOffset:endOffset])
}

func makeChunk(filePath, symbol, kind string, startLine, endLine int, content string) Chunk {
	// Hash filePath+content so chunks are unique even in minified files where
	// multiple symbols share the same line number.
	h := sha256.New()
	h.Write([]byte(filePath))
	h.Write([]byte{':'})
	h.Write([]byte(content))
	id := fmt.Sprintf("%x", h.Sum(nil))[:16]
	return Chunk{
		ID:        id,
		FilePath:  filePath,
		Symbol:    symbol,
		Kind:      kind,
		StartLine: startLine,
		EndLine:   endLine,
		Content:   content,
	}
}
