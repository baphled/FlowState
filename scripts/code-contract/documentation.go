package main

import (
	"fmt"
	"go/ast"
	"strings"
)

func documentationErrors(path string, file *ast.File) []string {
	var errors []string
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			if missingDoc(node.Name, node.Doc) {
				errors = append(errors, fmt.Sprintf("%s: exported %s needs a comment starting with its name", path, node.Name.Name))
			}
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				errors = append(errors, specificationDocs(path, node, spec)...)
				if typ, ok := spec.(*ast.TypeSpec); ok {
					errors = append(errors, interfaceDocs(path, typ)...)
				}
			}
		}
	}
	return errors
}

func specificationDocs(path string, group *ast.GenDecl, spec ast.Spec) []string {
	var names []*ast.Ident
	var doc *ast.CommentGroup
	switch node := spec.(type) {
	case *ast.TypeSpec:
		names = []*ast.Ident{node.Name}
		doc = node.Doc
	case *ast.ValueSpec:
		names = node.Names
		doc = node.Doc
	default:
		return nil
	}
	if doc == nil && len(group.Specs) == 1 {
		doc = group.Doc
	}
	var errors []string
	for _, name := range names {
		if missingDoc(name, doc) {
			errors = append(errors, fmt.Sprintf("%s: exported %s needs a comment starting with its name", path, name.Name))
		}
	}
	return errors
}

func missingDoc(name *ast.Ident, doc *ast.CommentGroup) bool {
	if !ast.IsExported(name.Name) {
		return false
	}
	if doc == nil {
		return true
	}
	return !strings.HasPrefix(strings.TrimSpace(doc.Text()), name.Name+" ")
}

func interfaceDocs(path string, typ *ast.TypeSpec) []string {
	value, ok := typ.Type.(*ast.InterfaceType)
	if !ok {
		return nil
	}
	var errors []string
	for _, field := range value.Methods.List {
		for _, name := range field.Names {
			if missingDoc(name, field.Doc) {
				errors = append(errors, fmt.Sprintf("%s: exported interface method %s needs its contract comment", path, name.Name))
			}
		}
	}
	return errors
}
