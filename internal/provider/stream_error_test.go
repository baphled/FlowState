package provider

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// P18a unit tests for StreamError and ClassifyStreamError.
//
// The classifier is a pragmatic, non-breaking addition at the consumer
// boundary: providers today return plain errors on stream chunks, and the
// consumer (the chat intent) needs a cheap way to decide whether an error
// is (a) a transient network blip worth surfacing inline, (b) a generic
// user-facing failure, or (c) a critical condition that must also reach
// stderr / structured logs.
//
// The classifier inspects a structured *provider.Error first (most
// reliable — its ErrorType is already populated by the boundary code).
// Failing that, it falls back to keyword matching against the error's
// message so that wrapped or stringly-typed errors still resolve to a
// sensible severity.

var _ = Describe("StreamError severity string", func() {
	It("names each severity distinctly for log attributes", func() {
		Expect(SeverityTransient.String()).To(Equal("transient"))
		Expect(SeverityUser.String()).To(Equal("user"))
		Expect(SeverityCritical.String()).To(Equal("critical"))
	})
})

var _ = Describe("StreamError.Error()", func() {
	It("returns the underlying error message verbatim when unprovidered", func() {
		se := &StreamError{
			Err:      errors.New("connection refused"),
			Severity: SeverityTransient,
		}
		Expect(se.Error()).To(Equal("connection refused"))
	})

	It("prefixes the provider name when present", func() {
		se := &StreamError{
			Err:      errors.New("API key invalid"),
			Severity: SeverityCritical,
			Provider: "anthropic",
		}
		Expect(se.Error()).To(Equal("anthropic: API key invalid"))
	})

	It("returns <nil> safely when the receiver is nil", func() {
		var se *StreamError
		Expect(se.Error()).To(Equal("<nil>"))
	})

	It("unwraps to the underlying error for errors.Is / errors.As", func() {
		inner := errors.New("boom")
		se := &StreamError{Err: inner, Severity: SeverityUser}
		Expect(errors.Is(se, inner)).To(BeTrue())
	})
})

var _ = Describe("ClassifyStreamError", func() {
	DescribeTable(
		"keyword fallback classification",
		func(msg string, want StreamErrorSeverity) {
			got := ClassifyStreamError(errors.New(msg))
			Expect(got.Severity).To(Equal(want))
			Expect(got.Err).To(HaveOccurred())
		},
		// Transient — generally retry-able network conditions.
		Entry("connection refused → transient", "dial tcp 127.0.0.1:8080: connection refused", SeverityTransient),
		Entry("connection reset → transient", "connection reset by peer", SeverityTransient),
		Entry("context deadline exceeded → transient", "context deadline exceeded", SeverityTransient),
		Entry("provider timeout → transient", "provider timeout after 30s", SeverityTransient),
		Entry("EOF during stream → transient", "unexpected EOF", SeverityTransient),

		// Critical — auth, config, quota. Requires user action; worth stderr.
		Entry("API key invalid → critical", "API key invalid", SeverityCritical),
		Entry("401 unauthorized → critical", "401 unauthorized", SeverityCritical),
		Entry("403 forbidden → critical", "403 forbidden", SeverityCritical),
		Entry("quota exceeded → critical", "quota exceeded for this month", SeverityCritical),
		Entry("authentication failure → critical", "authentication failure", SeverityCritical),

		// Anything else defaults to user-visible but non-critical.
		Entry("unknown error → user", "something went wrong", SeverityUser),
		Entry("parse error → user", "parse error at line 3", SeverityUser),
	)

	It("returns SeverityUser with the original error when input is nil", func() {
		got := ClassifyStreamError(nil)
		Expect(got).ToNot(HaveOccurred())
	})

	It("respects an existing *StreamError without re-wrapping", func() {
		original := &StreamError{
			Err:      errors.New("boom"),
			Severity: SeverityCritical,
			Provider: "openai",
		}
		got := ClassifyStreamError(original)
		Expect(got).To(BeIdenticalTo(original))
	})

	It("uses *provider.Error's ErrorType when available", func() {
		perr := &Error{
			ErrorType: ErrorTypeAuthFailure,
			Provider:  "anthropic",
			Message:   "invalid bearer token",
		}
		got := ClassifyStreamError(perr)
		Expect(got).To(HaveOccurred())
		Expect(got.Severity).To(Equal(SeverityCritical))
		Expect(got.Provider).To(Equal("anthropic"))
	})

	It("classifies provider ErrorTypeRateLimit as transient", func() {
		perr := &Error{
			ErrorType: ErrorTypeRateLimit,
			Provider:  "openai",
			Message:   "slow down",
		}
		got := ClassifyStreamError(perr)
		Expect(got.Severity).To(Equal(SeverityTransient))
	})

	It("classifies provider ErrorTypeQuota as critical", func() {
		perr := &Error{
			ErrorType: ErrorTypeQuota,
			Provider:  "anthropic",
			Message:   "monthly quota reached",
		}
		got := ClassifyStreamError(perr)
		Expect(got.Severity).To(Equal(SeverityCritical))
	})

	It("classifies a wrapped error by unwrapping to *provider.Error", func() {
		inner := &Error{
			ErrorType: ErrorTypeAuthFailure,
			Provider:  "zai",
			Message:   "bad key",
		}
		wrapped := fmt.Errorf("stream failed: %w", inner)
		got := ClassifyStreamError(wrapped)
		Expect(got.Severity).To(Equal(SeverityCritical))
	})
})

var _ = Describe("IsCriticalStreamError", func() {
	It("is true for a critical StreamError", func() {
		se := &StreamError{Err: errors.New("API key invalid"), Severity: SeverityCritical}
		Expect(IsCriticalStreamError(se)).To(BeTrue())
	})

	It("is true when classification resolves to critical", func() {
		Expect(IsCriticalStreamError(errors.New("401 unauthorized"))).To(BeTrue())
	})

	It("is false for transient errors", func() {
		Expect(IsCriticalStreamError(errors.New("connection refused"))).To(BeFalse())
	})

	It("is false for nil", func() {
		Expect(IsCriticalStreamError(nil)).To(BeFalse())
	})
})
