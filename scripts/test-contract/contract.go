package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

func checkSource(input sourceFile) []string {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, input.Name, input.Source, 0)
	if err != nil {
		return []string{err.Error()}
	}
	imports := map[string]string{}
	ginkgoDot, gomegaDot := false, false
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if path == "github.com/onsi/ginkgo/v2" {
			name = "ginkgo"
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = path
		if name == "." && path == "github.com/onsi/ginkgo/v2" {
			ginkgoDot = true
		}
		if name == "." && path == "github.com/onsi/gomega" {
			gomegaDot = true
		}
	}
	resolve := func(expr ast.Expr) string {
		switch x := expr.(type) {
		case *ast.Ident:
			if ginkgoDot || gomegaDot {
				return x.Name
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if imports[id.Name] == "github.com/onsi/ginkgo/v2" || imports[id.Name] == "github.com/onsi/gomega" {
					return x.Sel.Name
				}
				if imports[id.Name] == "time" && x.Sel.Name == "Sleep" {
					return "Sleep"
				}
			}
		}
		return ""
	}
	var errors []string
	fail := func(node ast.Node, msg string) {
		errors = append(errors, fmt.Sprintf("%s: %s", fs.Position(node.Pos()), msg))
	}
	subjects := map[*ast.CallExpr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := resolve(call.Fun)
		if name == "DescribeTableSubtree" {
			fail(call, "table subtrees are unsupported by the contract gate; use DescribeTable")
		}
		if name == "It" || name == "Specify" || name == "DescribeTable" {
			foundBody := false
			for _, arg := range call.Args {
				body, ok := arg.(*ast.FuncLit)
				if !ok {
					continue
				}
				foundBody = true
				count := 0
				ast.Inspect(body.Body, func(node ast.Node) bool {
					c, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					assertion := resolve(c.Fun)
					if assertion == "Expect" || assertion == "ExpectWithOffset" || assertion == "Eventually" || assertion == "Consistently" {
						subjects[c] = true
						terminal, timeout, polling := assertionChain(body.Body, c)
						if !terminal {
							fail(c, "expectation requires a terminal matcher")
						}
						if (assertion == "Eventually" || assertion == "Consistently") && (!timeout || !polling) {
							fail(c, "async expectations require explicit WithTimeout and WithPolling")
						}
						if !setupErrorCheck(body.Body, c) {
							count++
						}
					}
					return true
				})
				if count != 1 {
					fail(body, fmt.Sprintf("one behavioural expectation per spec required; got %d", count))
				}
			}
			if !foundBody {
				fail(call, "spec body must be an inline function checked by the contract gate")
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Name.Name == "TestMain" && !testMainBootstrap(node, imports) {
				fail(node, "TestMain must propagate the exit status of m.Run")
			}
			if strings.HasPrefix(node.Name.Name, "Test") && node.Name.Name != "TestMain" && !suiteBootstrap(node, resolve) {
				fail(node, "only a Ginkgo RunSpecs bootstrap may use TestX")
			}
		case *ast.CallExpr:
			name := resolve(node.Fun)
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Skip" || sel.Sel.Name == "Skipf" || sel.Sel.Name == "SkipNow" || sel.Sel.Name == "Parallel") {
				fail(node, "test skipping and t.Parallel are forbidden in Ginkgo specs")
			}
			if name == "Sleep" || name == "Skip" {
				fail(node, "sleep/skip is forbidden; use bounded async assertions and deterministic fixtures")
			}
			for _, bad := range []string{"FIt", "FSpecify", "FDescribe", "FContext", "FWhen", "FDescribeTable", "FEntry", "PIt", "PDescribe", "PContext", "PWhen", "PSpecify", "PDescribeTable", "PEntry", "XIt", "XDescribe", "XContext", "XWhen", "XSpecify", "XDescribeTable", "XEntry"} {
				if name == bad {
					fail(node, "focused/pending specs are forbidden")
				}
			}
			if (name == "Expect" || name == "ExpectWithOffset" || name == "Eventually" || name == "Consistently") && !subjects[node] && !setupErrorCheck(f, node) {
				fail(node, "behavioural expectations belong directly in a spec, not hidden helpers/hooks")
			}
		case *ast.Ident:
			if node.Name == "Focus" || node.Name == "Pending" || node.Name == "Serial" || node.Name == "Ordered" {
				fail(node, "focus/pending/serial/ordered require a separately reviewed policy change")
			}
		}
		return true
	})
	return errors
}

func setupErrorCheck(root ast.Node, assertion *ast.CallExpr) bool {
	if len(assertion.Args) != 1 {
		return false
	}
	id, ok := assertion.Args[0].(*ast.Ident)
	if !ok || id.Name != "err" && !strings.HasSuffix(id.Name, "Err") {
		return false
	}
	found := false
	ast.Inspect(root, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok || sel.X != assertion || (sel.Sel.Name != "NotTo" && sel.Sel.Name != "ToNot") || len(c.Args) != 1 {
			return true
		}
		m, ok := c.Args[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		switch x := m.Fun.(type) {
		case *ast.Ident:
			found = x.Name == "HaveOccurred"
		case *ast.SelectorExpr:
			found = x.Sel.Name == "HaveOccurred"
		}
		return !found
	})
	return found
}

func suiteBootstrap(f *ast.FuncDecl, resolve func(ast.Expr) string) bool {
	if f.Body == nil || len(f.Body.List) != 2 {
		return false
	}
	first, ok := f.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	second, ok := f.Body.List[1].(*ast.ExprStmt)
	if !ok {
		return false
	}
	a, ok := first.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	b, ok := second.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	if f.Type.Params == nil || len(f.Type.Params.List) != 1 || len(f.Type.Params.List[0].Names) != 1 {
		return false
	}
	param := f.Type.Params.List[0].Names[0].Name
	if len(a.Args) != 1 || len(b.Args) != 2 || resolve(a.Args[0]) != "Fail" {
		return false
	}
	arg, ok := b.Args[0].(*ast.Ident)
	return ok && arg.Name == param && resolve(a.Fun) == "RegisterFailHandler" && resolve(b.Fun) == "RunSpecs"
}

func assertionChain(root ast.Node, assertion *ast.CallExpr) (bool, bool, bool) {
	terminal, timeout, polling := false, false, false
	ast.Inspect(root, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		found := false
		ast.Inspect(sel.X, func(inner ast.Node) bool {
			if inner == assertion {
				found = true
			}
			return true
		})
		if !found {
			return true
		}
		switch sel.Sel.Name {
		case "To", "NotTo", "ToNot", "Should", "ShouldNot":
			terminal = len(c.Args) > 0
		case "WithTimeout":
			timeout = len(c.Args) == 1
		case "WithPolling":
			polling = len(c.Args) == 1
		}
		return true
	})
	return terminal, timeout, polling
}

func testMainBootstrap(f *ast.FuncDecl, imports map[string]string) bool {
	if f.Body == nil || len(f.Body.List) != 1 || f.Type.Params == nil || len(f.Type.Params.List) != 1 || len(f.Type.Params.List[0].Names) != 1 {
		return false
	}
	stmt, ok := f.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	exit, ok := stmt.X.(*ast.CallExpr)
	if !ok || len(exit.Args) != 1 {
		return false
	}
	sel, ok := exit.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Exit" {
		return false
	}
	qualifier, ok := sel.X.(*ast.Ident)
	if !ok || imports[qualifier.Name] != "os" {
		return false
	}
	run, ok := exit.Args[0].(*ast.CallExpr)
	if !ok || len(run.Args) != 0 {
		return false
	}
	method, ok := run.Fun.(*ast.SelectorExpr)
	if !ok || method.Sel.Name != "Run" {
		return false
	}
	receiver, ok := method.X.(*ast.Ident)
	return ok && receiver.Name == f.Type.Params.List[0].Names[0].Name
}
