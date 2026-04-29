package engine_test

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/runner"
	"github.com/baphled/flowstate/internal/tool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stubAutoresearchPruner is a test double for runner.AutoresearchPruner.
type stubAutoresearchPruner struct {
	result runner.AutoresearchPruneResult
	err    error
	// captured holds the opts passed to the most recent PruneAutoresearch call.
	captured runner.AutoresearchPruneOpts
}

func (s *stubAutoresearchPruner) PruneAutoresearch(
	_ context.Context,
	opts runner.AutoresearchPruneOpts,
) (runner.AutoresearchPruneResult, error) {
	s.captured = opts
	return s.result, s.err
}

var _ = Describe("AutoresearchPruneTool", func() {
	var (
		pruneTool *engine.AutoresearchPruneTool
		stub      *stubAutoresearchPruner
		ctx       context.Context
	)

	BeforeEach(func() {
		stub = &stubAutoresearchPruner{
			result: runner.AutoresearchPruneResult{
				RunsPruned:  2,
				KeysDeleted: 10,
				DryRun:      false,
				Runs:        []string{"run-abc", "run-def"},
			},
		}
		pruneTool = engine.NewAutoresearchPruneTool(stub)
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns 'autoresearch_prune'", func() {
			Expect(pruneTool.Name()).To(Equal("autoresearch_prune"))
		})
	})

	Describe("CanDelegate", func() {
		It("returns true", func() {
			Expect(pruneTool.CanDelegate()).To(BeTrue())
		})
	})

	Describe("Schema", func() {
		It("includes older_than, all, and dry_run properties", func() {
			schema := pruneTool.Schema()
			Expect(schema.Properties).To(HaveKey("older_than"))
			Expect(schema.Properties).To(HaveKey("all"))
			Expect(schema.Properties).To(HaveKey("dry_run"))
		})

		It("has no required fields", func() {
			schema := pruneTool.Schema()
			Expect(schema.Required).To(BeEmpty())
		})
	})

	Describe("Execute", func() {
		Context("with dry_run=true", func() {
			BeforeEach(func() {
				stub.result = runner.AutoresearchPruneResult{
					RunsPruned:  3,
					KeysDeleted: 15,
					DryRun:      true,
					Runs:        []string{"run-1", "run-2", "run-3"},
				}
			})

			It("returns JSON with dry_run=true and correct counts", func() {
				input := tool.Input{
					Name: "autoresearch_prune",
					Arguments: map[string]any{
						"dry_run": true,
					},
				}
				result, err := pruneTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var out map[string]any
				Expect(json.Unmarshal([]byte(result.Output), &out)).To(Succeed())
				Expect(out["dry_run"]).To(BeTrue())
				Expect(out["runs_pruned"]).To(BeEquivalentTo(3))
				Expect(out["keys_deleted"]).To(BeEquivalentTo(15))
			})

			It("passes dry_run=true to the pruner", func() {
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{"dry_run": true},
				}
				_, err := pruneTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(stub.captured.DryRun).To(BeTrue())
			})
		})

		Context("with all=true", func() {
			BeforeEach(func() {
				stub.result = runner.AutoresearchPruneResult{
					RunsPruned:  5,
					KeysDeleted: 25,
					DryRun:      false,
					Runs:        []string{"r1", "r2", "r3", "r4", "r5"},
				}
			})

			It("returns JSON listing all pruned runs", func() {
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{"all": true},
				}
				result, err := pruneTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var out map[string]any
				Expect(json.Unmarshal([]byte(result.Output), &out)).To(Succeed())
				Expect(out["runs_pruned"]).To(BeEquivalentTo(5))
				Expect(out["dry_run"]).To(BeFalse())
			})

			It("passes all=true to the pruner", func() {
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{"all": true},
				}
				_, err := pruneTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(stub.captured.All).To(BeTrue())
			})
		})

		Context("with older_than specified", func() {
			It("parses the duration and forwards it to the pruner", func() {
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{"older_than": "72h"},
				}
				_, err := pruneTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(stub.captured.OlderThan.Hours()).To(BeNumerically("==", 72))
			})

			It("returns an error for an invalid duration string", func() {
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{"older_than": "not-a-duration"},
				}
				_, err := pruneTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("older_than"))
			})
		})

		Context("when no arguments are supplied", func() {
			It("uses the default older_than of 168h", func() {
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{},
				}
				_, err := pruneTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(stub.captured.OlderThan.Hours()).To(BeNumerically("==", 168))
			})
		})

		Context("when the pruner is nil", func() {
			It("returns an error", func() {
				nilTool := engine.NewAutoresearchPruneTool(nil)
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{},
				}
				_, err := nilTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("pruner not configured"))
			})
		})

		Context("when the pruner returns an error", func() {
			BeforeEach(func() {
				stub.err = errors.New("coord-store unavailable")
			})

			It("propagates the error", func() {
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{},
				}
				_, err := pruneTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("coord-store unavailable"))
			})
		})

		Context("output JSON shape", func() {
			It("includes runs_pruned, keys_deleted, dry_run, and runs fields", func() {
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{},
				}
				result, err := pruneTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var out map[string]any
				Expect(json.Unmarshal([]byte(result.Output), &out)).To(Succeed())
				Expect(out).To(HaveKey("runs_pruned"))
				Expect(out).To(HaveKey("keys_deleted"))
				Expect(out).To(HaveKey("dry_run"))
				Expect(out).To(HaveKey("runs"))
			})

			It("returns an empty runs array (not null) when no runs were pruned", func() {
				stub.result = runner.AutoresearchPruneResult{
					RunsPruned:  0,
					KeysDeleted: 0,
					DryRun:      false,
					Runs:        nil,
				}
				input := tool.Input{
					Name:      "autoresearch_prune",
					Arguments: map[string]any{},
				}
				result, err := pruneTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var out map[string]any
				Expect(json.Unmarshal([]byte(result.Output), &out)).To(Succeed())
				runs, ok := out["runs"]
				Expect(ok).To(BeTrue())
				Expect(runs).NotTo(BeNil())
			})
		})
	})
})
