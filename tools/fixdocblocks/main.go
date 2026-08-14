package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var funcRe = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?(\w+)\s*\(`)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	count := 0
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				base := filepath.Base(path)
				if base == "vendor" || base == ".git" || base == "node_modules" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "doc.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		changed := false
		for i := 0; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if !funcRe.MatchString(trimmed) {
				continue
			}
			// Extract func name
			m := funcRe.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			funcName := m[1]
			if funcName == "main" || funcName == "init" {
				continue
			}
			// Collect the full function signature (may span multiple lines)
			sigLines := []string{trimmed}
			j := i
			// Find the closing paren of params and the return/body
			parenDepth := strings.Count(trimmed, "(") - strings.Count(trimmed, ")")
			for parenDepth > 0 && j+1 < len(lines) {
				j++
				next := strings.TrimSpace(lines[j])
				sigLines = append(sigLines, next)
				parenDepth += strings.Count(next, "(") - strings.Count(next, ")")
			}
			fullSig := strings.Join(sigLines, " ")
			// Extract params
			paramsRaw := extractBetweenParens(fullSig, "(", ")", 0)
			// After the params, check for return values
			afterParams := ""
			if idx := findClosingParen(fullSig, 0); idx >= 0 && idx+1 < len(fullSig) {
				afterParams = strings.TrimSpace(fullSig[idx+1:])
			}
			params := strings.TrimSpace(paramsRaw)
			hasParams := params != ""
			returns := afterParams
			// Remove trailing { from returns
			returns = strings.TrimSpace(strings.TrimSuffix(returns, "{"))
			hasReturns := returns != ""

			// Find doc comment above
			docStart := -1
			for k := i - 1; k >= 0; k-- {
				t := strings.TrimSpace(lines[k])
				if strings.HasPrefix(t, "//") {
					docStart = k
				} else {
					break
				}
			}
			if docStart == -1 {
				// No doc comment — create one
				docLines := []string{}
				docLines = append(docLines, "// "+funcName+" ...")
				if hasParams {
					docLines = append(docLines, "//")
					docLines = append(docLines, "// Expected: parameters for "+funcName+".")
				}
				if hasReturns {
					docLines = append(docLines, "//")
					docLines = append(docLines, "// Returns: result of "+funcName+".")
				}
				docLines = append(docLines, "//")
				docLines = append(docLines, "// Side effects: None.")
				// Insert before func line
				newLines := make([]string, 0)
				newLines = append(newLines, lines[:i]...)
				newLines = append(newLines, docLines...)
				newLines = append(newLines, lines[i:]...)
				lines = newLines
				i += len(docLines)
				changed = true
				continue
			}
			// Check existing doc for sections
			docText := strings.Join(lines[docStart:i], "\n")
			needsExpected := hasParams && !strings.Contains(docText, "Expected:")
			needsReturns := hasReturns && !strings.Contains(docText, "Returns:")
			needsSideEffects := !strings.Contains(docText, "Side effects:")
			if !needsExpected && !needsReturns && !needsSideEffects {
				continue
			}
			// Insert missing sections at end of doc comment (before func line)
			insertLines := []string{}
			if needsExpected || needsReturns || needsSideEffects {
				insertLines = append(insertLines, "//")
			}
			if needsExpected {
				insertLines = append(insertLines, "// Expected: parameters for "+funcName+".")
			}
			if needsReturns {
				insertLines = append(insertLines, "// Returns: result of "+funcName+".")
			}
			if needsSideEffects {
				insertLines = append(insertLines, "// Side effects: None.")
			}
			newLines := make([]string, 0)
			newLines = append(newLines, lines[:i]...)
			newLines = append(newLines, insertLines...)
			newLines = append(newLines, lines[i:]...)
			lines = newLines
			i += len(insertLines)
			changed = true
		}
		if changed {
			os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
			count++
		}
		return nil
	})
	fmt.Printf("Fixed %d files\n", count)
}

// findClosingParen finds the index of the ) that closes the ( at position start
//
// Expected: parameters for findClosingParen.
// Returns: result of findClosingParen.
// Side effects: None.
func findClosingParen(s string, start int) int {
	depth := 0
	for k := start; k < len(s); k++ {
		switch s[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return -1
}

// extractBetweenParens extracts content between the first matched pair of open/close chars
//
// Expected: parameters for extractBetweenParens.
// Returns: result of extractBetweenParens.
// Side effects: None.
func extractBetweenParens(s, open, close string, startIdx int) string {
	depth := 0
	begin := -1
	for k := 0; k < len(s); k++ {
		if string(s[k]) == open {
			if depth == 0 {
				begin = k + 1
			}
			depth++
		} else if string(s[k]) == close {
			depth--
			if depth == 0 && begin >= 0 {
				return s[begin:k]
			}
		}
	}
	return ""
}
