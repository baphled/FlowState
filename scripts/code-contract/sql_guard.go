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

func coreSQLBoundary(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) < 3 || parts[0] != "internal" {
		return false
	}
	switch parts[1] {
	case "app", "runtime", "session", "tool":
		return true
	case "provider":
		// internal/provider is a port; nested packages are adapters.
		return len(parts) == 3
	default:
		return false
	}
}

func inspectInlineSQL(f sourceFile) []error {
	if !coreSQLBoundary(f.Name) {
		return nil
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, f.Name, f.Source, parser.AllErrors)
	if err != nil {
		return []error{fmt.Errorf("%s: parse source: %w", f.Name, err)}
	}
	var findings []error
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && sqlStatement.MatchString(value) {
			line := positions.Position(literal.Pos()).Line
			findings = append(findings, fmt.Errorf("%s:%d: SQL statement in core layer; move SQL into a persistence adapter", f.Name, line))
		}
		return true
	})
	return findings
}
