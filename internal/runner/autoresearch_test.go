package runner_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/runner"
)

func TestRunner(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Runner Suite")
}

type fakeAutoresearchRunner struct {
	opts   runner.AutoresearchOpts
	out    io.Writer
	result runner.AutoresearchResult
	err    error
	calls  int
}

func (f *fakeAutoresearchRunner) RunAutoresearch(_ context.Context, opts runner.AutoresearchOpts, out io.Writer) (runner.AutoresearchResult, error) {
	f.calls++
	f.opts = opts
	f.out = out
	return f.result, f.err
}

type fakeAutoresearchPruner struct {
	opts   runner.AutoresearchPruneOpts
	result runner.AutoresearchPruneResult
	err    error
	calls  int
}

func (f *fakeAutoresearchPruner) PruneAutoresearch(_ context.Context, opts runner.AutoresearchPruneOpts) (runner.AutoresearchPruneResult, error) {
	f.calls++
	f.opts = opts
	return f.result, f.err
}

var _ runner.AutoresearchRunner = (*fakeAutoresearchRunner)(nil)
var _ runner.AutoresearchPruner = (*fakeAutoresearchPruner)(nil)

var _ = Describe("AutoresearchRunner interface", func() {
	Describe("RunAutoresearch", func() {
		It("returns the configured result and forwards opts/out to the spy", func() {
			want := runner.AutoresearchResult{
				RunID:             "run-1",
				TerminationReason: "converged",
				TotalTrials:       7,
				Converged:         true,
				BestScore:         0.91,
				BestCandidateSHA:  "abc123",
				BestCommitSHA:     "def456",
			}
			spy := &fakeAutoresearchRunner{result: want}
			opts := runner.AutoresearchOpts{
				Surface:         "tui",
				DriverScript:    "scripts/d.sh",
				EvaluatorScript: "scripts/e.sh",
				RunID:           "run-1",
				MaxTrials:       10,
				TimeBudget:      5 * time.Minute,
				MetricDirection: "min",
				DriverAgent:     "default-assistant",
				NoImproveWindow: 3,
				Program:         "autoresearch",
			}
			out := &bytes.Buffer{}

			got, err := spy.RunAutoresearch(context.Background(), opts, out)

			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
			Expect(spy.opts).To(Equal(opts))
			Expect(spy.out).To(BeIdenticalTo(out))
			Expect(spy.calls).To(Equal(1))
		})

		It("propagates errors verbatim to the caller", func() {
			wantErr := errors.New("boom")
			spy := &fakeAutoresearchRunner{err: wantErr}
			_, err := spy.RunAutoresearch(context.Background(), runner.AutoresearchOpts{}, io.Discard)
			Expect(err).To(MatchError(wantErr))
		})
	})
})

var _ = Describe("AutoresearchOpts", func() {
	It("carries every operator-settable field round-trip", func() {
		opts := runner.AutoresearchOpts{
			Surface:         "tui",
			DriverScript:    "scripts/d.sh",
			EvaluatorScript: "scripts/e.sh",
			RunID:           "run-2",
			MaxTrials:       4,
			TimeBudget:      2 * time.Hour,
			MetricDirection: "max",
			DriverAgent:     "dev",
			NoImproveWindow: 2,
			Program:         "autoresearch",
		}
		Expect(opts.Surface).To(Equal("tui"))
		Expect(opts.DriverScript).To(Equal("scripts/d.sh"))
		Expect(opts.EvaluatorScript).To(Equal("scripts/e.sh"))
		Expect(opts.RunID).To(Equal("run-2"))
		Expect(opts.MaxTrials).To(Equal(4))
		Expect(opts.TimeBudget).To(Equal(2 * time.Hour))
		Expect(opts.MetricDirection).To(Equal("max"))
		Expect(opts.DriverAgent).To(Equal("dev"))
		Expect(opts.NoImproveWindow).To(Equal(2))
		Expect(opts.Program).To(Equal("autoresearch"))
	})

	It("defaults numeric and duration fields to their zero values", func() {
		var opts runner.AutoresearchOpts
		Expect(opts.MaxTrials).To(BeZero())
		Expect(opts.TimeBudget).To(BeZero())
		Expect(opts.NoImproveWindow).To(BeZero())
	})
})

var _ = Describe("AutoresearchResult", func() {
	It("carries the run outcome fields round-trip", func() {
		res := runner.AutoresearchResult{
			RunID:             "run-3",
			TerminationReason: "budget-exhausted",
			TotalTrials:       12,
			Converged:         false,
			BestScore:         0.42,
			BestCandidateSHA:  "c-1",
			BestCommitSHA:     "g-1",
		}
		Expect(res.RunID).To(Equal("run-3"))
		Expect(res.TerminationReason).To(Equal("budget-exhausted"))
		Expect(res.TotalTrials).To(Equal(12))
		Expect(res.Converged).To(BeFalse())
		Expect(res.BestScore).To(Equal(0.42))
		Expect(res.BestCandidateSHA).To(Equal("c-1"))
		Expect(res.BestCommitSHA).To(Equal("g-1"))
	})
})

var _ = Describe("AutoresearchPruner interface", func() {
	Describe("PruneAutoresearch", func() {
		It("returns the configured result and forwards opts to the spy", func() {
			want := runner.AutoresearchPruneResult{
				RunsPruned:  3,
				KeysDeleted: 17,
				DryRun:      false,
				Runs:        []string{"run-a", "run-b", "run-c"},
			}
			spy := &fakeAutoresearchPruner{result: want}
			opts := runner.AutoresearchPruneOpts{
				OlderThan: 24 * time.Hour,
				All:       false,
				DryRun:    false,
			}

			got, err := spy.PruneAutoresearch(context.Background(), opts)

			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
			Expect(spy.opts).To(Equal(opts))
			Expect(spy.calls).To(Equal(1))
		})

		It("propagates errors verbatim to the caller", func() {
			wantErr := errors.New("prune-failed")
			spy := &fakeAutoresearchPruner{err: wantErr}
			_, err := spy.PruneAutoresearch(context.Background(), runner.AutoresearchPruneOpts{})
			Expect(err).To(MatchError(wantErr))
		})
	})
})

var _ = Describe("AutoresearchPruneOpts", func() {
	It("carries OlderThan, All, and DryRun fields round-trip", func() {
		opts := runner.AutoresearchPruneOpts{
			OlderThan: 48 * time.Hour,
			All:       true,
			DryRun:    true,
		}
		Expect(opts.OlderThan).To(Equal(48 * time.Hour))
		Expect(opts.All).To(BeTrue())
		Expect(opts.DryRun).To(BeTrue())
	})

	It("defaults to a zero duration, All=false, and DryRun=false", func() {
		var opts runner.AutoresearchPruneOpts
		Expect(opts.OlderThan).To(BeZero())
		Expect(opts.All).To(BeFalse())
		Expect(opts.DryRun).To(BeFalse())
	})
})

var _ = Describe("AutoresearchPruneResult", func() {
	It("carries counts, dry-run flag, and run-id list round-trip", func() {
		res := runner.AutoresearchPruneResult{
			RunsPruned:  2,
			KeysDeleted: 9,
			DryRun:      true,
			Runs:        []string{"run-x", "run-y"},
		}
		Expect(res.RunsPruned).To(Equal(2))
		Expect(res.KeysDeleted).To(Equal(9))
		Expect(res.DryRun).To(BeTrue())
		Expect(res.Runs).To(Equal([]string{"run-x", "run-y"}))
	})

	It("defaults Runs to nil", func() {
		var res runner.AutoresearchPruneResult
		Expect(res.Runs).To(BeNil())
		Expect(res.RunsPruned).To(BeZero())
	})
})
