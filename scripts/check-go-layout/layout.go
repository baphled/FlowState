package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

func inspectSource(path string, source []byte) []string {
	if strings.HasSuffix(path, "_test.go") {
		return nil
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, 0)
	if err != nil {
		return []string{fmt.Sprintf("%s: parse: %v", path, err)}
	}
	base := filepath.Base(path)
	typesFile := base == "types.go" || strings.HasSuffix(base, "_types.go")
	var findings []string
	for _, declaration := range file.Decls {
		findings = append(findings, inspectDeclaration(declaration, typesFile, positions, file.Name.Name)...)
	}
	return findings
}

func inspectDeclaration(declaration ast.Decl, typesFile bool, positions *token.FileSet, owner string) []string {
	position := positions.Position(declaration.Pos())
	if _, ok := declaration.(*ast.FuncDecl); ok && typesFile {
		return []string{fmt.Sprintf("%s: functions and methods belong in implementation files", position)}
	}
	group, ok := declaration.(*ast.GenDecl)
	if !ok {
		return nil
	}
	if typesFile && group.Tok == token.VAR {
		return []string{fmt.Sprintf("%s: mutable variables belong in implementation files", position)}
	}
	if typesFile || group.Tok != token.TYPE {
		return nil
	}
	var findings []string
	for _, specification := range group.Specs {
		item, ok := specification.(*ast.TypeSpec)
		if !ok {
			continue
		}
		findings = append(findings, fmt.Sprintf("%s: move type %s to types.go or *_types.go in package %s", positions.Position(item.Pos()), item.Name.Name, owner))
	}
	return findings
}
