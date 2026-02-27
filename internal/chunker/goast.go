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

	// Package doc comment chunk.
	if file.Doc != nil {
		start := fset.Position(file.Doc.Pos())
		pkgEnd := fset.Position(file.Name.End())
		chunks = append(chunks, makeChunk(
			filePath, "package "+file.Name.Name, "package",
			start.Line, pkgEnd.Line,
			sliceContent(content, start.Offset, pkgEnd.Offset),
		))
	} else {
		start := fset.Position(file.Package)
		end := fset.Position(file.Name.End())
		chunks = append(chunks, makeChunk(
			filePath, "package "+file.Name.Name, "package",
			start.Line, end.Line,
			sliceContent(content, start.Offset, end.Offset),
		))
	}

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
			chunks = append(chunks, makeChunk(filePath, s.Name.Name, kind,
				start.Line, end.Line, sliceContent(content, start.Offset, end.Offset)))

		case *ast.ValueSpec:
			kind := "var"
			if d.Tok == token.CONST {
				kind = "const"
			}
			symbol := s.Names[0].Name
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
			chunks = append(chunks, makeChunk(filePath, symbol, kind,
				start.Line, end.Line, sliceContent(content, start.Offset, end.Offset)))
		}
	}

	return chunks
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
	raw := fmt.Sprintf("%s:%s:%d", filePath, symbol, startLine)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	return Chunk{
		ID:        hash[:16],
		FilePath:  filePath,
		Symbol:    symbol,
		Kind:      kind,
		StartLine: startLine,
		EndLine:   endLine,
		Content:   content,
	}
}
