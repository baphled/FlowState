package app_test

import (
	"context"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

var _ = Describe("ConfigureLogging", func() {
	ctx := context.Background()

	It("sets the default logger without panicking", func() {
		Expect(func() { app.ConfigureLogging("debug") }).NotTo(Panic())
	})

	It("accepts all valid log levels", func() {
		for _, level := range []string{"debug", "info", "warn", "error"} {
			Expect(func() { app.ConfigureLogging(level) }).NotTo(Panic())
		}
	})

	It("accepts unrecognised values without panicking", func() {
		Expect(func() { app.ConfigureLogging("unknown") }).NotTo(Panic())
	})

	It("sets debug level when configured", func() {
		app.ConfigureLogging("debug")
		Expect(slog.Default().Enabled(ctx, slog.LevelDebug)).To(BeTrue())
	})

	It("disables debug when level is info", func() {
		app.ConfigureLogging("info")
		Expect(slog.Default().Enabled(ctx, slog.LevelDebug)).To(BeFalse())
		Expect(slog.Default().Enabled(ctx, slog.LevelInfo)).To(BeTrue())
	})

	It("sets warn level correctly", func() {
		app.ConfigureLogging("warn")
		Expect(slog.Default().Enabled(ctx, slog.LevelInfo)).To(BeFalse())
		Expect(slog.Default().Enabled(ctx, slog.LevelWarn)).To(BeTrue())
	})

	It("sets error level correctly", func() {
		app.ConfigureLogging("error")
		Expect(slog.Default().Enabled(ctx, slog.LevelWarn)).To(BeFalse())
		Expect(slog.Default().Enabled(ctx, slog.LevelError)).To(BeTrue())
	})

	It("handles case-insensitive input", func() {
		app.ConfigureLogging("DEBUG")
		Expect(slog.Default().Enabled(ctx, slog.LevelDebug)).To(BeTrue())
	})

	It("defaults to info for empty string", func() {
		app.ConfigureLogging("")
		Expect(slog.Default().Enabled(ctx, slog.LevelInfo)).To(BeTrue())
		Expect(slog.Default().Enabled(ctx, slog.LevelDebug)).To(BeFalse())
	})
})
