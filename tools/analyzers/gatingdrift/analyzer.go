// Package gatingdrift provides a static analyser that flags struct
// fields whose godoc names a gating identifier (e.g. "declared in
// Capabilities.MCPServers") when the enclosing package no longer reads
// that identifier — Guard 3 of the review-pattern guards.
//
// Why: commit b960869 ("wire MCP tools to bypass manifest whitelist")
// stripped the manifest gate from buildAllowedToolSet but left the
// docstring on Config.MCPServerTools claiming the gate still applied.
// The reviewer (the same author) had no oracle to disagree with the
// behaviour change, and the test was rewritten to pin it. This analyser
// would have refused the commit by reporting that the docstring's
// gating identifier was no longer present anywhere in the engine
// package.
//
// Scope: deliberately narrow per the user's "200 LOC ceiling, ship
// narrow" directive. The analyser:
//
//  1. Walks every struct type declaration.
//  2. For each field with a doc comment, extracts gating identifiers of
//     the form "<Phrase> <Capitalised>.<Capitalised>" where <Phrase> is
//     one of: "declared in", "gated by", "controlled by", "filtered by".
//     The phrase requirement keeps false positives down — incidental
//     mentions of dotted identifiers (e.g. "see foo.Bar for context")
//     are not flagged.
//  3. For each gating identifier, checks whether any *ast.SelectorExpr
//     in the same package has matching X / Sel names.
//  4. Reports a diagnostic on the struct type when the identifier is
//     named but not read.
//
// Out of scope (explicit known gaps, see commit body for rationale):
//
//   - Cross-package gates (the gating identifier might live in another
//     package). The b960869 case is intra-package, so this is a useful
//     start.
//   - Type-aware resolution. We compare names only; a field named
//     "MCPServers" on an unrelated type still counts as a read.
//   - Graceful handling of dot-imported packages.
package gatingdrift

import (
	"go/ast"
	"go/token"
	"regexp"
	"strings"

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
	X        string  // left-hand identifier (e.g. "Capabilities")
	Sel      string  // right-hand selector (e.g. "MCPServers")
	StructAt token.Pos // position to report against (the struct decl)
	Field    string  // field name carrying the docstring
	Owner    string  // owning struct name
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
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				for _, field := range st.Fields.List {
					if field.Doc == nil || len(field.Names) == 0 {
						continue
					}
					doc := field.Doc.Text()
					matches := gatingPhrasePattern.FindAllStringSubmatch(doc, -1)
					for _, m := range matches {
						refs = append(refs, gatingRef{
							X:        m[2],
							Sel:      m[3],
							StructAt: ts.Pos(),
							Field:    field.Names[0].Name,
							Owner:    ts.Name.Name,
						})
					}
				}
			}
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
	return strings.Join([]string{x, sel}, ".")
}
