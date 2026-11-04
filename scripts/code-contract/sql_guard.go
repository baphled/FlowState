package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// sqlStatement matches common SQL prefixes, not arbitrary prose.
// Composed SQL still needs independent architectural review.
var sqlStatement = regexp.MustCompile(`(?is)^\s*(?:SELECT\s+(?:.+?\bFROM\b|\d+\b|\*)|INSERT\s+INTO\b|UPDATE\s+\S+\s+SET\b|DELETE\s+FROM\b|WITH\s+.+?\b(?:SELECT|INSERT|UPDATE|DELETE)\b|(?:CREATE|ALTER|DROP)\s+(?:TABLE|INDEX)\b|PRAGMA\s+\w+)`)

// sqlBoundary includes adapters: moving SQL out of core does not make it compliant.
// Untouched legacy files are handled by the committed-diff source gate.
func sqlBoundary(name string) bool {
	parts := strings.Split(name, "/")
	return len(parts) >= 3 && parts[0] == "internal"
}

// directSQLCall catches common direct database entry points even when SQL
// is constructed elsewhere. Non-database Exec methods remain permitted.
func directSQLCall(sel *ast.SelectorExpr) bool {
	switch sel.Sel.Name {
	case "Query", "QueryContext", "QueryRow", "QueryRowContext",
		"Prepare", "PrepareContext", "Raw":
		return true
	case "Exec", "ExecContext":
		receiver, ok := sel.X.(*ast.Ident)
		if !ok {
			return false
		}
		switch receiver.Name {
		case "db", "tx", "conn", "sqlDB", "pool", "mapped", "gormDB", "orm", "store":
			return true
		}
	}
	return false
}

// inspectInlineSQL rejects raw SQL literals and common execution APIs in
// changed production packages. It does not replace semantic ORM review.
func inspectInlineSQL(f sourceFile) []error {
	if !sqlBoundary(f.Name) {
		return nil
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, f.Name, f.Source, parser.AllErrors)
	if err != nil {
		return []error{fmt.Errorf("%s: parse source: %w", f.Name, err)}
	}
	var findings []error
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				return true
			}
			sql, err := strconv.Unquote(value.Value)
			if err == nil && sqlStatement.MatchString(sql) {
				line := positions.Position(value.Pos()).Line
				findings = append(findings, fmt.Errorf("%s:%d: raw SQL statement forbidden; use an ORM model", f.Name, line))
			}
		case *ast.CallExpr:
			if selector, ok := value.Fun.(*ast.SelectorExpr); ok && directSQLCall(selector) {
				line := positions.Position(selector.Pos()).Line
				findings = append(findings, fmt.Errorf("%s:%d: direct query API forbidden; use ORM operations", f.Name, line))
			}
		}
		return true
	})
	return findings
}
