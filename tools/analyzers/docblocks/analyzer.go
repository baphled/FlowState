// Package docblocks provides a static analyser that checks for missing
// or malformed documentation blocks in Go code.
package docblocks

import (
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Analyzer enforces structured doc comments on all exported symbols.
//
// Expected:
//   - Pass must contain valid Go files.
//
// Returns:
//   - nil interface and nil error on success.
//
// Side effects:
//   - Reports diagnostics via pass.Reportf for violations.
var Analyzer = &analysis.Analyzer{
	Name: "docblocks",
	Doc:  "enforce structured doc comments on exported symbols",
	Run:  run,
}

// visibilityPrefix returns "exported" for exported identifiers and "unexported" for unexported ones.
//
// Expected:
//   - name must be a valid Go identifier.
//
// Returns:
//   - "exported" or "unexported" string.
//
// Side effects:
//   - None.
func visibilityPrefix(name *ast.Ident) string {
	if name.IsExported() {
		return "exported"
	}
	return "unexported"
}

// run analyses Go source files and reports documentation violations.
//
// Expected:
//   - pass must contain valid Go files.
//
// Returns:
//   - nil interface and nil error on success.
//
// Side effects:
//   - Reports diagnostics via pass.Reportf for violations.
func run(pass *analysis.Pass) (interface{}, error) {
	hasDocGo := false
	for _, file := range pass.Files {
		if isTestFile(pass, file) {
			continue
		}
		if isDocGoFile(pass, file) {
			hasDocGo = true
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				checkFuncDecl(pass, d)
			case *ast.GenDecl:
				checkGenDecl(pass, d)
			}
		}
	}
	checkPackageDoc(pass, hasDocGo)
	return nil, nil //nolint:nilnil // go/analysis framework requires (interface{}, error) return
}

// checkFuncDecl validates documentation for a function or method declaration.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - fn must be a non-nil function declaration.
//
// Side effects:
//   - Reports diagnostics for missing or malformed doc comments.
func checkFuncDecl(pass *analysis.Pass, fn *ast.FuncDecl) {
	if isExcludedFuncName(fn.Name.Name) {
		return
	}

	visibility := visibilityPrefix(fn.Name)
	kind := funcKind(fn)

	if fn.Doc == nil {
		pass.Reportf(fn.Pos(), "%s %s %s missing doc comment", visibility, kind, fn.Name.Name)
		return
	}

	text := fn.Doc.Text()

	checkNamePrefix(pass, fn.Pos(), text, fn.Name.Name)
	checkReturnSection(pass, fn, visibility, kind, text)
	checkExpectedSection(pass, fn, visibility, kind, text)
	checkSideEffectsSection(pass, fn.Pos(), visibility, kind, fn.Name.Name, text)
}

// checkGenDecl routes general declarations to appropriate type or value checkers.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - decl must be a non-nil general declaration.
//
// Side effects:
//   - May report diagnostics via delegated check functions.
func checkGenDecl(pass *analysis.Pass, decl *ast.GenDecl) {
	switch decl.Tok {
	case token.TYPE:
		checkTypeDecl(pass, decl)
	case token.CONST, token.VAR:
		checkValueDecl(pass, decl)
	}
}

// checkTypeDecl validates documentation for type declarations.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - decl must be a type declaration.
//
// Side effects:
//   - Reports diagnostics for missing or malformed doc comments.
func checkTypeDecl(pass *analysis.Pass, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}

		visibility := visibilityPrefix(ts.Name)
		doc := specDoc(ts.Doc, decl.Doc)
		if doc == nil {
			pass.Reportf(ts.Pos(), "%s type %s missing doc comment", visibility, ts.Name.Name)
			continue
		}

		checkNamePrefix(pass, ts.Pos(), doc.Text(), ts.Name.Name)
	}
}

// checkValueDecl validates documentation for const and var declarations.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - decl must be a const or var declaration.
//
// Side effects:
//   - Reports diagnostics for missing or malformed doc comments.
func checkValueDecl(pass *analysis.Pass, decl *ast.GenDecl) {
	kind := tokenKind(decl.Tok)
	grouped := decl.Lparen.IsValid()

	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}

		for _, name := range vs.Names {
			if !name.IsExported() {
				continue
			}

			doc := resolveValueDoc(grouped, vs.Doc, decl.Doc)
			if doc == nil {
				pass.Reportf(name.Pos(), "exported %s %s missing doc comment", kind, name.Name)
				continue
			}

			if !isGroupDoc(grouped, vs.Doc, decl.Doc) {
				checkNamePrefix(pass, name.Pos(), doc.Text(), name.Name)
			}
		}
	}
}

// checkNamePrefix validates that a doc comment starts with the symbol name.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - pos must be a valid source position.
//   - text must be the doc comment text.
//   - name must be the symbol name.
//
// Side effects:
//   - Reports diagnostic if doc comment does not start with name.
func checkNamePrefix(pass *analysis.Pass, pos token.Pos, text string, name string) {
	if !strings.HasPrefix(text, name) {
		pass.Reportf(pos, "doc comment for %s should start with \"%s\"", name, name)
	}
}

// checkReturnSection validates that functions with return values have a Returns section.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - fn must be a non-nil function declaration.
//   - visibility must be "exported" or "unexported".
//   - kind must be "function" or "method".
//   - text must be the doc comment text.
//
// Side effects:
//   - Reports diagnostic if Returns section is missing.
func checkReturnSection(pass *analysis.Pass, fn *ast.FuncDecl, visibility string, kind string, text string) {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return
	}

	if !hasSection(text, "Returns:") {
		pass.Reportf(fn.Pos(), "%s %s %s missing Returns: section", visibility, kind, fn.Name.Name)
	}
}

// checkExpectedSection validates that functions with parameters have an Expected section.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - fn must be a non-nil function declaration.
//   - visibility must be "exported" or "unexported".
//   - kind must be "function" or "method".
//   - text must be the doc comment text.
//
// Side effects:
//   - Reports diagnostic if Expected section is missing.
func checkExpectedSection(pass *analysis.Pass, fn *ast.FuncDecl, visibility string, kind string, text string) {
	if !hasParameters(fn) {
		return
	}

	if !hasSection(text, "Expected:") {
		pass.Reportf(fn.Pos(), "%s %s %s missing Expected: section", visibility, kind, fn.Name.Name)
	}
}

// checkSideEffectsSection validates that all functions have a Side effects section.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - pos must be a valid source position.
//   - visibility must be "exported" or "unexported".
//   - kind must be "function" or "method".
//   - name must be the function name.
//   - text must be the doc comment text.
//
// Side effects:
//   - Reports diagnostic if Side effects section is missing.
func checkSideEffectsSection(pass *analysis.Pass, pos token.Pos, visibility string, kind string, name string, text string) {
	if !hasSection(text, "Side effects:") {
		pass.Reportf(pos, "%s %s %s missing Side effects: section", visibility, kind, name)
	}
}

// hasSection checks if a doc comment contains a specific section header.
//
// Expected:
//   - text must be the doc comment text.
//   - section must be the section header to search for.
//
// Returns:
//   - true if the section is found, false otherwise.
//
// Side effects:
//   - None.
func hasSection(text string, section string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == section || strings.HasPrefix(trimmed, section) {
			return true
		}
	}
	return false
}

// hasParameters checks if a function has any parameters.
//
// Expected:
//   - fn must be a non-nil function declaration.
//
// Returns:
//   - true if the function has parameters, false otherwise.
//
// Side effects:
//   - None.
func hasParameters(fn *ast.FuncDecl) bool {
	return fn.Type.Params != nil && len(fn.Type.Params.List) > 0
}

// funcKind determines whether a function declaration is a function or method.
//
// Expected:
//   - fn must be a non-nil function declaration.
//
// Returns:
//   - "method" if the function has a receiver, "function" otherwise.
//
// Side effects:
//   - None.
func funcKind(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return "method"
	}
	return "function"
}

// tokenKind returns a human-readable kind for const and var tokens.
//
// Expected:
//   - tok must be a valid token.
//
// Returns:
//   - "const" for CONST, "var" for VAR, "value" otherwise.
//
// Side effects:
//   - None.
func tokenKind(tok token.Token) string {
	switch tok {
	case token.CONST:
		return "const"
	case token.VAR:
		return "var"
	default:
		return "value"
	}
}

// isExcludedFuncName checks if a function name should be excluded from documentation checks.
//
// Expected:
//   - name must be a valid function name.
//
// Returns:
//   - true for "main" and "init" functions, false otherwise.
//
// Side effects:
//   - None.
func isExcludedFuncName(name string) bool {
	return name == "main" || name == "init"
}

// isTestFile checks if a file is a Go test file.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - file must be a valid AST file.
//
// Returns:
//   - true if the filename ends with "_test.go", false otherwise.
//
// Side effects:
//   - None.
func isTestFile(pass *analysis.Pass, file *ast.File) bool {
	filename := pass.Fset.Position(file.Pos()).Filename
	return strings.HasSuffix(filename, "_test.go")
}

// isDocGoFile checks if a file is a doc.go file.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - file must be a valid AST file.
//
// Returns:
//   - true if the filename ends with "doc.go", false otherwise.
//
// Side effects:
//   - None.
func isDocGoFile(pass *analysis.Pass, file *ast.File) bool {
	filename := pass.Fset.Position(file.Pos()).Filename
	return strings.HasSuffix(filename, "doc.go")
}

// checkPackageDoc validates that a package has a doc.go file.
//
// Expected:
//   - pass must be a valid analysis pass.
//   - hasDocGo must indicate whether a doc.go file was found.
//
// Side effects:
//   - Reports diagnostic if doc.go file is missing for non-test, non-main packages.
func checkPackageDoc(pass *analysis.Pass, hasDocGo bool) {
	pkgName := pass.Pkg.Name()
	if strings.HasSuffix(pkgName, "_test") || pkgName == "main" {
		return
	}
	if !hasDocGo {
		pass.Reportf(token.NoPos, "package %s missing doc.go file with package-level documentation", pkgName)
	}
}

// specDoc returns the doc comment for a type spec, preferring spec-level over decl-level.
//
// Expected:
//   - specDoc is the spec-level doc comment (may be nil).
//   - declDoc is the decl-level doc comment (may be nil).
//
// Returns:
//   - specDoc if non-nil, otherwise declDoc.
//
// Side effects:
//   - None.
func specDoc(specDoc *ast.CommentGroup, declDoc *ast.CommentGroup) *ast.CommentGroup {
	if specDoc != nil {
		return specDoc
	}
	return declDoc
}

// resolveValueDoc determines the appropriate doc comment for a value spec.
//
// Expected:
//   - grouped indicates if this is a grouped declaration.
//   - specDoc is the spec-level doc comment (may be nil).
//   - declDoc is the decl-level doc comment (may be nil).
//
// Returns:
//   - The appropriate doc comment based on grouping rules.
//
// Side effects:
//   - None.
func resolveValueDoc(grouped bool, specDoc *ast.CommentGroup, declDoc *ast.CommentGroup) *ast.CommentGroup {
	if grouped {
		if specDoc != nil {
			return specDoc
		}
		if declDoc != nil {
			return declDoc
		}
		return nil
	}
	return declDoc
}

// isGroupDoc checks if a doc comment applies to a grouped declaration.
//
// Expected:
//   - grouped indicates if this is a grouped declaration.
//   - specDoc is the spec-level doc comment (may be nil).
//   - declDoc is the decl-level doc comment (may be nil).
//
// Returns:
//   - true if this is a grouped declaration using the decl-level doc.
//
// Side effects:
//   - None.
func isGroupDoc(grouped bool, specDoc *ast.CommentGroup, declDoc *ast.CommentGroup) bool {
	return grouped && specDoc == nil && declDoc != nil
}
