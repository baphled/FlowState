package main

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Inline SQL architecture guard", func() {
	DescribeTable("core boundary classification",
		func(name string, expected bool) {
			Expect(coreSQLBoundary(name)).To(Equal(expected))
		},
		Entry("application is core", "internal/app/usecase.go", true),
		Entry("nested runtime is core", "internal/runtime/loop/turn.go", true),
		Entry("session is core", "internal/session/session.go", true),
		Entry("tool policy is core", "internal/tool/policy.go", true),
		Entry("provider port is core", "internal/provider/port.go", true),
		Entry("provider transport is adapter", "internal/provider/zai/client.go", false),
		Entry("storage is adapter", "internal/store/sqlite.go", false),
		Entry("unrelated tooling", "scripts/check.go", false),
	)

	DescribeTable("literal SQL detection",
		func(path, code string, rejected bool) {
			found := inspectInlineSQL(sourceFile{Name: path, Source: code})
			Expect(len(found) > 0).To(Equal(rejected))
		},
		Entry("select in runtime", "internal/runtime/service.go", "package runtime\nvar q = `SELECT id FROM events`", true),
		Entry("insert in application", "internal/app/usecase.go", "package app\nvar q = `INSERT INTO sessions(id) VALUES (1)`", true),
		Entry("update in session", "internal/session/session.go", "package session\nvar q = `UPDATE sessions SET state = 1`", true),
		Entry("delete in tool", "internal/tool/executor.go", "package tool\nvar q = `DELETE FROM runs`", true),
		Entry("multiline select", "internal/runtime/loop/turn.go", "package loop\nvar q = `\n SELECT id\n FROM events`", true),
		Entry("non-SQL text remains legal", "internal/app/usecase.go", "package app\nvar text = `Select the active model`", false),
		Entry("adapter may own SQL", "internal/store/sqlite.go", "package store\nvar q = `SELECT id FROM events`", false),
		Entry("transport is exempt", "internal/provider/zai/client.go", "package zai\nvar q = `SELECT id FROM events`", false),
	)

	It("reports exact source locations for a rejected SQL literal", func() {
		source := sourceFile{Name: "internal/app/work.go", Source: "package app\nvar q = `SELECT id FROM runs`"}
		found := inspectInlineSQL(source)
		Expect(fmt.Sprint(found)).To(ContainSubstring("internal/app/work.go:2:"))
	})

	It("fails closed on an unparseable core source", func() {
		source := sourceFile{Name: "internal/runtime/invalid.go", Source: "package runtime\nfunc {"}
		found := inspectInlineSQL(source)
		Expect(fmt.Sprint(found)).To(ContainSubstring("parse source"))
	})
})
