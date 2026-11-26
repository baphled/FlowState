package app

import (
	"fmt"

	"github.com/baphled/flowstate/internal/session"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ErrorCode for the hook errors", func() {
	It("maps ErrHookRefused onto hook_refused", func() {
		Expect(ErrorCode(ErrHookRefused)).To(Equal(CodeHookRefused))
	})
	It("maps a wrapped ErrHookRefused onto hook_refused", func() {
		Expect(ErrorCode(fmt.Errorf("hook h1: %w", ErrHookRefused))).To(Equal("hook_refused"))
	})
	It("maps ErrHookStop onto hook_stop", func() {
		Expect(ErrorCode(ErrHookStop)).To(Equal(CodeHookStop))
	})
	It("maps a wrapped ErrHookStop onto hook_stop", func() {
		Expect(ErrorCode(fmt.Errorf("hook h1: %w", ErrHookStop))).To(Equal("hook_stop"))
	})
	It("keeps budget_exceeded when ErrBudgetExceeded wraps ErrHookStop", func() {
		Expect(ErrorCode(fmt.Errorf("%w: %w", ErrBudgetExceeded, ErrHookStop))).To(Equal(CodeBudgetExceeded))
	})
	It("keeps budget_exceeded when ErrHookStop wraps ErrBudgetExceeded", func() {
		Expect(ErrorCode(fmt.Errorf("%w: %w", ErrHookStop, ErrBudgetExceeded))).To(Equal(CodeBudgetExceeded))
	})
	It("keeps max_turns when ErrMaxTurns and ErrHookStop are joined", func() {
		Expect(ErrorCode(fmt.Errorf("%w: %w", ErrHookStop, ErrMaxTurns))).To(Equal(CodeMaxTurns))
	})
	It("keeps output_truncated when ErrOutputTruncated and ErrHookStop are joined", func() {
		Expect(ErrorCode(fmt.Errorf("%w: %w", ErrHookStop, ErrOutputTruncated))).To(Equal(CodeOutputTruncated))
	})
	It("keeps usage when ErrHookRefused and ErrUsage are joined", func() {
		Expect(ErrorCode(fmt.Errorf("%w: %w", ErrHookRefused, session.ErrUsage))).To(Equal(CodeUsage))
	})
})
