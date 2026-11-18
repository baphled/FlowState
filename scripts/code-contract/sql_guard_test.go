package main

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Inline SQL architecture guard", func() {
	DescribeTable("core boundary classification",
		func(name string, expected bool) {
			Expect(sqlBoundary(name)).To(Equal(expected))
		},
		Entry("application is core", "internal/app/usecase.go", true),
		Entry("nested runtime is core", "internal/runtime/loop/turn.go", true),
		Entry("session is core", "internal/session/session.go", true),
		Entry("tool policy is core", "internal/tool/policy.go", true),
		Entry("provider port is core", "internal/provider/port.go", true),
		Entry("provider transport is production", "internal/provider/zai/client.go", true),
		Entry("storage is production", "internal/store/sqlite.go", true),
		Entry("unrelated tooling", "scripts/check.go", false),
	)

	DescribeTable("literal SQL detection",
		func(path, code string, expectedCount int) {
			found := inspectInlineSQL(sourceFile{Name: path, Source: code})
			Expect(found).To(HaveLen(expectedCount))
		},
		Entry("select in runtime", "internal/runtime/service.go", "package runtime\nvar q = `SELECT id FROM events`", 1),
		Entry("insert in application", "internal/app/usecase.go", "package app\nvar q = `INSERT INTO sessions(id) VALUES (1)`", 1),
		Entry("update in session", "internal/session/session.go", "package session\nvar q = `UPDATE sessions SET state = 1`", 1),
		Entry("delete in tool", "internal/tool/executor.go", "package tool\nvar q = `DELETE FROM runs`", 1),
		Entry("multiline select", "internal/runtime/loop/turn.go", "package loop\nvar q = `\n SELECT id\n FROM events`", 1),
		Entry("migration DDL in adapter", "internal/storage/migrate.go", "package storage\nvar ddl = `CREATE TABLE runs (id TEXT)`", 1),
		Entry("ORM model insert permitted", "internal/storage/receipts.go", "package storage\nfunc f(db interface{ Create(any) error }, row any) { _ = db.Create(row) }", 0),
		Entry("non-SQL text remains legal", "internal/app/usecase.go", "package app\nvar text = `Select the active model`", 0),
		Entry("adapter cannot own raw SQL", "internal/store/sqlite.go", "package store\nvar q = `SELECT id FROM events`", 1),
		Entry("transport cannot own raw SQL", "internal/provider/zai/client.go", "package zai\nvar q = `SELECT id FROM events`", 1),
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
	DescribeTable("direct query API detection",
		func(path, code string, expected int) {
			findings := inspectInlineSQL(sourceFile{Name: path, Source: code})
			Expect(findings).To(HaveLen(expected))
		},
		Entry("dynamic SQL through Exec", "internal/storage/queries.go", "package storage\nfunc f(db interface{ Exec(string) error }, q string) { _ = db.Exec(q) }", 1),
		Entry("QueryRow in storage", "internal/storage/read.go", "package storage\nfunc f(db interface{ QueryRow(string) any }, q string) { _ = db.QueryRow(q) }", 1),
		Entry("GORM Raw in storage", "internal/storage/read.go", "package storage\nfunc f(orm interface{ Raw(string) any }, q string) { _ = orm.Raw(q) }", 1),
		Entry("non-database Exec remains legal", "internal/tool/command.go", "package tool\nfunc f(command interface{ Exec() error }) { _ = command.Exec() }", 0),
		Entry("tools outside production", "scripts/runner.go", "package main\nvar q = `SELECT id FROM events`", 0),
	)

})
