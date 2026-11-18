package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strconv"
)

func importPaths(f *ast.File) map[string]string {
	paths := map[string]string{}
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if imp.Name != nil {
			paths[imp.Name.Name] = path
			continue
		}
		if path == "os" {
			paths["os"] = path
		}
	}
	return paths
}

// checkSpecReturns excludes nested callbacks, whose returns do not exit the spec.
func checkSpecReturns(body *ast.FuncLit, fail func(ast.Node, string)) {
	ast.Inspect(body.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if ret, ok := n.(*ast.ReturnStmt); ok && len(ret.Results) == 0 {
			fail(ret, "spec return can bypass its behavioural expectation")
		}
		return true
	})
}

// checkOracle recognises explicit syntax only; it is not a semantic proof.
func checkOracle(call *ast.CallExpr, assertion string, fail func(ast.Node, string)) {
	index := 0
	if assertion == "ExpectWithOffset" {
		index = 1
	}
	if len(call.Args) <= index {
		return
	}
	subject := unparen(call.Args[index])
	if tautology(subject) {
		fail(subject, "tautological subject is not a behavioural oracle")
	}
	literal, ok := subject.(*ast.CompositeLit)
	if !ok {
		return
	}
	array, ok := literal.Type.(*ast.ArrayType)
	if !ok {
		return
	}
	if explicitAny(array.Elt) && len(literal.Elts) > 1 {
		fail(literal, "any slice/array subjects can bundle unrelated behaviours; assert a typed contract")
	}
}

func explicitAny(expr ast.Expr) bool {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name == "any"
	}
	iface, ok := expr.(*ast.InterfaceType)
	return ok && len(iface.Methods.List) == 0
}

func unparen(expr ast.Expr) ast.Expr {
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = p.X
	}
}

func sameExpression(a, b ast.Expr) bool {
	if !stableExpression(a) || !stableExpression(b) {
		return false
	}
	var left, right bytes.Buffer
	if format.Node(&left, token.NewFileSet(), unparen(a)) != nil {
		return false
	}
	if format.Node(&right, token.NewFileSet(), unparen(b)) != nil {
		return false
	}
	return left.String() == right.String()
}

func negates(a, b ast.Expr) bool {
	unary, ok := unparen(a).(*ast.UnaryExpr)
	return ok && unary.Op == token.NOT && sameExpression(unary.X, b)
}

func tautology(expr ast.Expr) bool {
	binary, ok := unparen(expr).(*ast.BinaryExpr)
	if !ok || binary.Op != token.LOR {
		return false
	}
	if negates(binary.X, binary.Y) || negates(binary.Y, binary.X) {
		return true
	}
	left, lok := unparen(binary.X).(*ast.BinaryExpr)
	right, rok := unparen(binary.Y).(*ast.BinaryExpr)
	if !lok || !rok {
		return false
	}
	opposite := left.Op == token.EQL && right.Op == token.NEQ || left.Op == token.NEQ && right.Op == token.EQL
	return opposite && sameExpression(left.X, right.X) && sameExpression(left.Y, right.Y)
}

func isAssertion(name string) bool {
	switch name {
	case "Expect", "ExpectWithOffset", "Eventually", "Consistently":
		return true
	}
	return false
}

func forbiddenMethod(name string) bool {
	switch name {
	case "Skip", "Skipf", "SkipNow", "Parallel":
		return true
	}
	return false
}

func singleParameter(f *ast.FuncDecl) bool {
	return f.Type.Params != nil && len(f.Type.Params.List) == 1 && len(f.Type.Params.List[0].Names) == 1
}

func singleStatement(f *ast.FuncDecl) bool {
	return f.Body != nil && len(f.Body.List) == 1
}

// stableExpression avoids calling repeated evaluations tautologies when calls may differ.
func stableExpression(expr ast.Expr) bool {
	switch node := unparen(expr).(type) {
	case *ast.Ident, *ast.BasicLit:
		return true
	case *ast.SelectorExpr:
		return stableExpression(node.X)
	case *ast.UnaryExpr:
		return node.Op == token.NOT && stableExpression(node.X)
	case *ast.BinaryExpr:
		return stableExpression(node.X) && stableExpression(node.Y)
	}
	return false
}
