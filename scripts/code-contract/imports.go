package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"strconv"
	"strings"
)

const modulePath = "github.com/baphled/flowstate/"

func importErrors(path string, file *ast.File) []string {
	var errors []string
	for _, spec := range file.Imports {
		name, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: malformed import", path))
			continue
		}
		if !allowedImport(path, name) {
			errors = append(errors, fmt.Sprintf("%s: import %s violates the owning-layer contract", path, name))
		}
	}
	for _, comment := range file.Comments {
		for _, line := range comment.List {
			text := strings.TrimSpace(strings.TrimLeft(line.Text, "/*"))
			if strings.HasPrefix(text, "nolint") || strings.HasPrefix(text, "revive:disable") || strings.HasPrefix(text, "lint:ignore") || strings.HasPrefix(text, "gocyclo:ignore") || strings.HasPrefix(text, "funlen:ignore") {
				errors = append(errors, path+": inline contract waivers are forbidden")
			}
		}
	}
	return errors
}

func within(path, owner string) bool { return path == owner || strings.HasPrefix(path, owner+"/") }

func allowedImport(path, name string) bool {
	internal := strings.TrimPrefix(name, modulePath)
	if strings.HasPrefix(path, "internal/") && within(internal, "cmd") {
		return false
	}
	if strings.HasPrefix(path, "cmd/") {
		if strings.HasPrefix(name, modulePath) {
			return within(internal, "internal/app") || within(internal, "internal/config")
		}
		return standardImport(name)
	}
	if isDomain(path) {
		return allowedDomainImport(path, name)
	}
	if isAdapter(path) {
		return !withinAny(internal, []string{"internal/app", "internal/runtime", "cmd"})
	}
	return true
}

func isDomain(path string) bool {
	if withinAny(path, []string{"internal/runtime", "internal/session", "internal/tool"}) {
		return true
	}
	return strings.HasPrefix(path, "internal/provider/") && !strings.Contains(strings.TrimPrefix(path, "internal/provider/"), "/")
}

func isAdapter(path string) bool {
	return withinAny(path, []string{"internal/store", "internal/storage", "internal/oauth"}) || strings.HasPrefix(path, "internal/provider/")
}

func withinAny(path string, owners []string) bool {
	for _, owner := range owners {
		if within(path, owner) {
			return true
		}
	}
	return false
}

func allowedDomainImport(path, name string) bool {
	internal := strings.TrimPrefix(name, modulePath)
	if strings.HasPrefix(name, modulePath) {
		return internal == "internal/provider" || internal == "internal/session" || internal == "internal/tool" || sameDomain(path, internal)
	}
	if withinAny(name, []string{"net/http", "database/sql"}) {
		return false
	}
	return standardImport(name)
}

func sameDomain(source, target string) bool {
	for _, owner := range []string{"internal/runtime", "internal/session", "internal/tool"} {
		if within(source, owner) && within(target, owner) {
			return true
		}
	}
	return false
}

func standardImport(name string) bool {
	pkg, err := build.Default.Import(name, "", build.FindOnly)
	return err == nil && pkg.Goroot
}
