package gatingdrift

import (
	"go/ast"
	"go/token"
	"regexp"

	"golang.org/x/tools/go/analysis"
)

// Analyzer is the gatingdrift singlechecker entry point. See the package
// godoc for the rules and known gaps.
//
// Expected:
//   - pass.Files holds Go source files for a single package.
//
// Returns:
//   - nil interface and nil error on success.
//
// Side effects:
//   - Reports diagnostics via pass.Reportf for each detected drift.
var Analyzer = &analysis.Analyzer{
	Name: "gatingdrift",
	Doc:  "flag struct fields whose docstring names a gating identifier the package never reads",
	Run:  run,
}

// gatingPhrasePattern matches the canonical phrasings that signal a
// docstring is asserting a gating relationship. Kept conservative on
// purpose — this is the false-positive control valve.
var gatingPhrasePattern = regexp.MustCompile(
	`(?i)(declared in|gated by|controlled by|filtered by)\s+([A-Z][A-Za-z0-9_]*)\.([A-Z][A-Za-z0-9_]*)`,
)

// gatingRef captures one gating identifier discovered in a docstring.
type gatingRef struct {
	X        string    // left-hand identifier (e.g. "Capabilities")
	Sel      string    // right-hand selector (e.g. "MCPServers")
	StructAt token.Pos // position to report against (the struct decl)
	Field    string    // field name carrying the docstring
	Owner    string    // owning struct name
}

// run is the analyser entry point. See package godoc for the
// algorithm.
//
// Expected:
//   - pass.Files holds parsed Go files for one package.
//
// Returns:
//   - nil interface and nil error.
//
// Side effects:
//   - Calls pass.Reportf for every detected drift.
func run(pass *analysis.Pass) (interface{}, error) {
	refs := collectGatingRefs(pass)
	if len(refs) == 0 {
		return nil, nil
	}

	reads := collectSelectorReads(pass)

	for _, ref := range refs {
		if reads[selectorKey(ref.X, ref.Sel)] {
			continue
		}
		pass.Reportf(
			ref.StructAt,
			"%s.%s docstring names gating identifier %q but the enclosing package never reads it",
			ref.Owner, ref.Field, ref.X+"."+ref.Sel,
		)
	}

	return nil, nil
}

// collectGatingRefs walks every struct field doc comment and harvests
// gating identifiers of the form "<phrase> <X>.<Sel>".
//
// Expected:
//   - pass.Files is non-nil.
//
// Returns:
//   - A slice of gatingRef, one per identifier found.
//
// Side effects:
//   - None.
func collectGatingRefs(pass *analysis.Pass) []gatingRef {
	var refs []gatingRef
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			refs = append(refs, refsFromDecl(decl)...)
		}
	}
	return refs
}

// refsFromDecl returns every gatingRef derived from the field-doc gating
// phrases inside a single top-level declaration. Non-struct type blocks
// yield the empty slice.
//
// Expected:
//   - decl is a top-level declaration from a file in the pass.
//
// Returns:
//   - The gatingRef slice for each (struct, field) pair whose doc comment
//     mentions a gating identifier. Empty for non-type / non-struct decls.
//
// Side effects:
//   - None.
func refsFromDecl(decl ast.Decl) []gatingRef {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.TYPE {
		return nil
	}
	var refs []gatingRef
	for _, spec := range gen.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			continue
		}
		refs = append(refs, refsFromStruct(ts, st)...)
	}
	return refs
}

// refsFromStruct collects gatingRefs for each field whose doc comment
// names a gating identifier.
//
// Expected:
//   - ts names the enclosing struct type; st holds its field list.
//
// Returns:
//   - One gatingRef per (field, gating-phrase match).
//
// Side effects:
//   - None.
func refsFromStruct(ts *ast.TypeSpec, st *ast.StructType) []gatingRef {
	var refs []gatingRef
	for _, field := range st.Fields.List {
		if field.Doc == nil || len(field.Names) == 0 {
			continue
		}
		for _, m := range gatingPhrasePattern.FindAllStringSubmatch(field.Doc.Text(), -1) {
			refs = append(refs, gatingRef{
				X:        m[2],
				Sel:      m[3],
				StructAt: ts.Pos(),
				Field:    field.Names[0].Name,
				Owner:    ts.Name.Name,
			})
		}
	}
	return refs
}

// collectSelectorReads walks the package and indexes every
// "X.Sel" selector expression where both segments are simple
// identifiers.
//
// Expected:
//   - pass.Files is non-nil.
//
// Returns:
//   - A set of "X.Sel" composite keys present in the package.
//
// Side effects:
//   - None.
func collectSelectorReads(pass *analysis.Pass) map[string]bool {
	reads := make(map[string]bool)
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				// Chained selector: e.g. e.manifest.Capabilities.MCPServers.
				// Walk one step further so we still catch the gating
				// segment in the middle of the chain.
				inner, ok := sel.X.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				reads[selectorKey(inner.Sel.Name, sel.Sel.Name)] = true
				return true
			}
			reads[selectorKey(ident.Name, sel.Sel.Name)] = true
			return true
		})
	}
	return reads
}

// selectorKey forms the composite map key used by collectSelectorReads.
//
// Expected:
//   - x and sel are non-empty Go identifiers.
//
// Returns:
//   - A "X.Sel" string suitable for map lookup.
//
// Side effects:
//   - None.
func selectorKey(x, sel string) string {
	return x + "." + sel
}
