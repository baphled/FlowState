package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Owning-package structure", func() {
	DescribeTable("checks production declarations", func(path, source string, errors int) {
		Expect(inspectSource(path, []byte(source))).To(HaveLen(errors))
	},
		Entry("new internal owner needs contract", "internal/runtime_extra/types.go", "package runtime_extra; type event struct{}", 1),
		Entry("nested known owner", "internal/runtime/nested/types.go", "package nested; type event struct{}", 0),
		Entry("unregistered public root", "pkg/engine/engine.go", "package engine;func work(){}", 1),
		Entry("type beside implementation", "scripts/fixture/service.go", "package service; type record struct{}", 1),
		Entry("type alias beside implementation", "scripts/fixture/service.go", "package service; type record = string", 1),
		Entry("owning type file", "scripts/fixture/types.go", "package service; type record struct{}", 0),
		Entry("specific type file", "scripts/fixture/event_types.go", "package service; type event struct{}", 0),
		Entry("methods elsewhere", "scripts/fixture/types.go", "package service; func run(){}", 1),
		Entry("mutable state elsewhere", "scripts/fixture/types.go", "package service; var state int", 1),
		Entry("related constants allowed", "scripts/fixture/types.go", "package service; const active=1", 0),
		Entry("fixture types exempt", "scripts/fixture/service_test.go", "package service; type fixture struct{}", 0),
		Entry("catch-all package forbidden", "scripts/fixture/types.go", "package common; type record struct{}", 1),
		Entry("malformed source", "scripts/fixture/types.go", "package", 1),
	)
})

var _ = Describe("Layer dependencies", func() {
	DescribeTable("checks every production file regardless of build constraints", func(path, name string, allowed bool) {
		Expect(allowedImport(path, name)).To(Equal(allowed))
	},
		Entry("CLI platform SDK forbidden", "cmd/flowstate/main_freebsd.go", "example.com/sdk", false),
		Entry("CLI standard library", "cmd/flowstate/main.go", "context", true),
		Entry("domain port", "internal/runtime/engine.go", modulePath+"internal/provider", true),
		Entry("domain cannot import storage", "internal/runtime/engine.go", modulePath+"internal/store", false),
		Entry("nested domain cannot import storage", "internal/runtime/child/service.go", modulePath+"internal/store", false),
		Entry("domain cannot import transport", "internal/session/session_windows.go", "net/http", false),
		Entry("domain cannot import SQL", "internal/session/types.go", "database/sql/driver", false),
		Entry("domain cannot import SDK", "internal/runtime/a.go", "example.com/sdk", false),
		Entry("domain uses standard library", "internal/runtime/a.go", "context", true),
		Entry("CLI uses application", "cmd/flowstate/main.go", modulePath+"internal/app", true),
		Entry("CLI cannot import concrete store", "cmd/flowstate/main.go", modulePath+"internal/store", false),
		Entry("adapter cannot import lifecycle", "internal/provider/zai/deep/a.go", modulePath+"internal/runtime/child", false),
		Entry("adapter imports port", "internal/provider/zai/a.go", modulePath+"internal/provider", true),
		Entry("internal never imports command", "internal/config/a.go", modulePath+"cmd/flowstate", false),
	)
	It("rejects inline rule suppression", func() {
		Expect(inspectSource("scripts/fixture/service.go", []byte("package service\n//nolint:depguard\nfunc work(){}"))).To(HaveLen(1))
	})
})

var _ = Describe("Public source documentation", func() {
	DescribeTable("checks parsed exported declarations even behind build tags", func(path, source string, errors int) {
		Expect(inspectSource(path, []byte(source))).To(HaveLen(errors))
	},
		Entry("interface method docs required", "scripts/fixture/types.go", "package service\n// Worker handles bounded work.\ntype Worker interface { Run() }", 1),
		Entry("interface method documented", "scripts/fixture/types.go", "package service\n// Worker handles bounded work.\ntype Worker interface {\n// Run executes one item.\nRun()\n}", 0),
		Entry("function requires documentation", "scripts/fixture/service.go", "package service;func Open(){}", 1),
		Entry("function names its contract", "scripts/fixture/service.go", "package service\n// Open creates an isolated session.\nfunc Open(){}", 0),
		Entry("wrong comment name rejected", "scripts/fixture/service.go", "package service\n// Work creates a session.\nfunc Open(){}", 1),
		Entry("public type needs doc", "scripts/fixture/types.go", "package service;type Event struct{}", 1),
		Entry("public alias needs doc", "scripts/fixture/types.go", "package service;type Event = string", 1),
		Entry("public constant needs doc", "scripts/fixture/constants.go", "package service;const Active=1", 1),
		Entry("platform file docs checked", "scripts/fixture/service_windows.go", "//go:build windows\n\npackage service;func Open(){}", 1),
	)
})

var _ = Describe("Metric waivers", func() {
	It("rejects a cyclomatic-complexity waiver", func() {
		Expect(inspectSource("scripts/fixture/service.go", []byte("package service\n//gocyclo:ignore\nfunc work(){}"))).To(HaveLen(1))
	})
})
