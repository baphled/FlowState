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

func checkFuncDecl(pass *analysis.Pass, fn *ast.FuncDecl) {
	if !fn.Name.IsExported() {
		return
	}

	if isExcludedFuncName(fn.Name.Name) {
		return
	}

	kind := funcKind(fn)

	if fn.Doc == nil {
		pass.Reportf(fn.Pos(), "exported %s %s missing doc comment", kind, fn.Name.Name)
		return
	}

	text := fn.Doc.Text()

	checkNamePrefix(pass, fn.Pos(), text, fn.Name.Name)
	checkReturnSection(pass, fn, kind, text)
	checkExpectedSection(pass, fn, kind, text)
	checkSideEffectsSection(pass, fn.Pos(), kind, fn.Name.Name, text)
}

func checkGenDecl(pass *analysis.Pass, decl *ast.GenDecl) {
	switch decl.Tok {
	case token.TYPE:
		checkTypeDecl(pass, decl)
	case token.CONST, token.VAR:
		checkValueDecl(pass, decl)
	}
}

func checkTypeDecl(pass *analysis.Pass, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok || !ts.Name.IsExported() {
			continue
		}

		doc := specDoc(ts.Doc, decl.Doc)
		if doc == nil {
			pass.Reportf(ts.Pos(), "exported type %s missing doc comment", ts.Name.Name)
			continue
		}

		checkNamePrefix(pass, ts.Pos(), doc.Text(), ts.Name.Name)
	}
}

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

func checkNamePrefix(pass *analysis.Pass, pos token.Pos, text string, name string) {
	if !strings.HasPrefix(text, name) {
		pass.Reportf(pos, "doc comment for %s should start with \"%s\"", name, name)
	}
}

func checkReturnSection(pass *analysis.Pass, fn *ast.FuncDecl, kind string, text string) {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return
	}

	if !hasSection(text, "Returns:") {
		pass.Reportf(fn.Pos(), "exported %s %s missing Returns: section", kind, fn.Name.Name)
	}
}

func checkExpectedSection(pass *analysis.Pass, fn *ast.FuncDecl, kind string, text string) {
	if !hasParameters(fn) {
		return
	}

	if !hasSection(text, "Expected:") {
		pass.Reportf(fn.Pos(), "exported %s %s missing Expected: section", kind, fn.Name.Name)
	}
}

func checkSideEffectsSection(pass *analysis.Pass, pos token.Pos, kind string, name string, text string) {
	if !hasSection(text, "Side effects:") {
		pass.Reportf(pos, "exported %s %s missing Side effects: section", kind, name)
	}
}

func hasSection(text string, section string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == section || strings.HasPrefix(trimmed, section) {
			return true
		}
	}
	return false
}

func hasParameters(fn *ast.FuncDecl) bool {
	return fn.Type.Params != nil && len(fn.Type.Params.List) > 0
}

func funcKind(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return "method"
	}
	return "function"
}

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

func isExcludedFuncName(name string) bool {
	return name == "main" || name == "init"
}

func isTestFile(pass *analysis.Pass, file *ast.File) bool {
	filename := pass.Fset.Position(file.Pos()).Filename
	return strings.HasSuffix(filename, "_test.go")
}

func isDocGoFile(pass *analysis.Pass, file *ast.File) bool {
	filename := pass.Fset.Position(file.Pos()).Filename
	return strings.HasSuffix(filename, "doc.go")
}

func checkPackageDoc(pass *analysis.Pass, hasDocGo bool) {
	pkgName := pass.Pkg.Name()
	if strings.HasSuffix(pkgName, "_test") || pkgName == "main" {
		return
	}
	if !hasDocGo {
		pass.Reportf(token.NoPos, "package %s missing doc.go file with package-level documentation", pkgName)
	}
}

func specDoc(specDoc *ast.CommentGroup, declDoc *ast.CommentGroup) *ast.CommentGroup {
	if specDoc != nil {
		return specDoc
	}
	return declDoc
}

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

func isGroupDoc(grouped bool, specDoc *ast.CommentGroup, declDoc *ast.CommentGroup) bool {
	return grouped && specDoc == nil && declDoc != nil
}
