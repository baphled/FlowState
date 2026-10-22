package bash_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
)

const descendantSettle = 500 * time.Millisecond

const executeReturnBudget = 8 * time.Second

const teardownPollBudget = 3 * time.Second

var descendantCommand = `bash -c 'exec -a %s sleep 300' & wait`

func markerAlive(marker string) bool {
	err := exec.Command("pgrep", "-f", marker).Run()
	return err == nil
}

func killMarker(marker string) {
	_ = exec.Command("pkill", "-KILL", "-f", marker).Run()
}

var _ = Describe("Bash Tool", func() {
	var bashTool *bash.Tool

	BeforeEach(func() {
		bashTool = bash.New()
	})

	Describe("Name", func() {
		It("returns bash", func() {
			Expect(bashTool.Name()).To(Equal("bash"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(bashTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has command in Required", func() {
			schema := bashTool.Schema()
			Expect(schema.Required).To(ContainElement("command"))
		})
	})

	Describe("Execute", func() {
		Context("with a valid command", func() {
			It("returns the command output", func() {
				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{"command": "echo hello"},
				}
				result, err := bashTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("hello"))
				Expect(result.Error).ToNot(HaveOccurred())
			})
		})

		Context("with an invalid command", func() {
			It("returns non-nil Error in result", func() {
				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{"command": "nonexistent_command_xyz_123"},
				}
				result, err := bashTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("with missing command argument", func() {
			It("returns a Go error", func() {
				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{},
				}
				_, err := bashTool.Execute(context.Background(), input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("with cancelled context", func() {
			It("respects context cancellation", func() {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{"command": "sleep 10"},
				}
				result, err := bashTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("with output exceeding the truncation cap", func() {
			It("returns a truncated payload with the recovery hint embedded", func() {
				// 60KB of output via printf + repeat.
				input := tool.Input{
					Name: "bash",
					Arguments: map[string]interface{}{
						"command": `awk 'BEGIN{for(i=0;i<3500;i++) print "line-" i}'`,
					},
				}
				result, err := bashTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("truncated"))
				Expect(result.Output).To(ContainSubstring("grep"))
				Expect(result.Output).To(ContainSubstring("offset"))
			})
		})

		Context("when a descendant holds the pipes and the context is cancelled", func() {
			It("returns promptly with the whole process tree dead", func() {
				marker := fmt.Sprintf("fsbkilltest%d", os.Getpid())
				DeferCleanup(func() { killMarker(marker) })

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{"command": fmt.Sprintf(descendantCommand, marker)},
				}

				type outcome struct {
					result tool.Result
					err    error
				}
				done := make(chan outcome, 1)
				go func() {
					result, err := bashTool.Execute(ctx, input)
					done <- outcome{result: result, err: err}
				}()

				time.Sleep(descendantSettle)
				cancel()

				var out outcome
				Eventually(done, executeReturnBudget).Should(Receive(&out))
				Expect(out.err).NotTo(HaveOccurred())
				Expect(out.result.Error).To(HaveOccurred())
				Expect(errors.Is(out.result.Error, context.Canceled)).To(BeTrue())

				deadline := time.Now().Add(teardownPollBudget)
				for time.Now().Before(deadline) && markerAlive(marker) {
					time.Sleep(100 * time.Millisecond)
				}
				Expect(markerAlive(marker)).To(BeFalse())
			})
		})

		Context("with normal piped commands", func() {
			It("returns the pipeline output unchanged", func() {
				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{"command": `printf 'a\nb\n' | wc -l`},
				}
				result, err := bashTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("2"))
			})
		})
	})
})

var _ = Describe("Bash tool timeout configuration", func() {
	Describe("DefaultTimeout", func() {
		It("is 300 seconds", func() {
			Expect(bash.DefaultTimeout).To(Equal(300 * time.Second))
		})
	})

	Describe("constructors", func() {
		It("New uses DefaultTimeout", func() {
			Expect(bash.New().Timeout()).To(Equal(bash.DefaultTimeout))
		})

		It("NewWithGuard uses DefaultTimeout", func() {
			Expect(bash.NewWithGuard(nil).Timeout()).To(Equal(bash.DefaultTimeout))
		})

		It("NewWithTimeout uses the supplied budget", func() {
			Expect(bash.NewWithTimeout(90 * time.Second).Timeout()).To(Equal(90 * time.Second))
		})

		It("NewWithGuardTimeout uses the supplied budget and preserves the guard", func() {
			t := bash.NewWithGuardTimeout(nil, 2*time.Second)
			Expect(t.Timeout()).To(Equal(2 * time.Second))
			Expect(t.Guard()).To(BeNil())
		})

		It("non-positive timeouts fall back to DefaultTimeout", func() {
			Expect(bash.NewWithTimeout(0).Timeout()).To(Equal(bash.DefaultTimeout))
			Expect(bash.NewWithTimeout(-time.Second).Timeout()).To(Equal(bash.DefaultTimeout))
			Expect(bash.NewWithGuardTimeout(nil, 0).Timeout()).To(Equal(bash.DefaultTimeout))
		})
	})

	Describe("Execute under a custom timeout", func() {
		It("kills a command that exceeds the configured budget and tags the error", func() {
			bashTool := bash.NewWithTimeout(500 * time.Millisecond)
			input := tool.Input{
				Name:      "bash",
				Arguments: map[string]interface{}{"command": "sleep 30"},
			}
			start := time.Now()
			result, err := bashTool.Execute(context.Background(), input)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
			Expect(errors.Is(result.Error, bash.ErrDeadlineExceeded)).To(BeTrue())
			Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second))
		})

		It("lets a short command finish under a tight budget", func() {
			bashTool := bash.NewWithTimeout(5 * time.Second)
			input := tool.Input{
				Name:      "bash",
				Arguments: map[string]interface{}{"command": "echo ok"},
			}
			result, err := bashTool.Execute(context.Background(), input)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(Equal("ok"))
			Expect(result.Error).NotTo(HaveOccurred())
		})

		It("does not tag parent-context cancellation as a deadline kill", func() {
			bashTool := bash.NewWithTimeout(30 * time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			input := tool.Input{
				Name:      "bash",
				Arguments: map[string]interface{}{"command": "sleep 10"},
			}
			result, err := bashTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
			Expect(errors.Is(result.Error, context.Canceled)).To(BeTrue())
		})
	})
})
