package batch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/batch"
)

// fakeTool is a controllable tool double used to verify the batch tool's
// concurrency and error-aggregation behaviour. It optionally signals when
// Execute starts (via started) and blocks on a release channel so the test
// can observe in-flight calls before letting them complete.
type fakeTool struct {
	name    string
	output  string
	err     error
	started chan<- string
	release <-chan struct{}
}

func (f fakeTool) Name() string         { return f.name }
func (f fakeTool) Description() string  { return "fake" }
func (f fakeTool) Schema() tool.Schema  { return tool.Schema{Type: "object"} }
func (f fakeTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	if f.started != nil {
		f.started <- f.name
	}
	if f.release != nil {
		<-f.release
	}
	if f.err != nil {
		return tool.Result{}, f.err
	}
	return tool.Result{Output: f.output}, nil
}

// Batch tool tests cover three behaviours:
//   - basic metadata reporting and a no-op execution with empty tools list,
//   - concurrent execution: two registered tools must both observe Execute
//     start before either is allowed to complete (proves they don't run
//     sequentially),
//   - partial-failure preservation: when one tool errors and another
//     succeeds, the aggregate result must surface both the per-tool error
//     and the successful output.
var _ = Describe("Batch tool", func() {
	Describe("metadata and empty execution", func() {
		It("reports name, description, schema and runs with empty tools list", func() {
			toolUnderTest := batch.New(tool.NewRegistry())
			Expect(toolUnderTest.Name()).To(Equal("batch"))
			Expect(toolUnderTest.Description()).NotTo(BeEmpty())
			Expect(toolUnderTest.Schema().Type).To(Equal("object"))

			result, err := toolUnderTest.Execute(context.Background(), tool.Input{
				Name:      "batch",
				Arguments: map[string]any{"tools": []any{}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
		})
	})

	Describe("Execute with multiple tools", func() {
		It("runs registered calls concurrently", func() {
			started := make(chan string, 2)
			release := make(chan struct{})
			registry := tool.NewRegistry()
			registry.Register(fakeTool{name: "first", output: "one", started: started, release: release})
			registry.Register(fakeTool{name: "second", output: "two", started: started, release: release})

			toolUnderTest := batch.New(registry)
			done := make(chan struct {
				result tool.Result
				err    error
			}, 1)

			go func() {
				result, err := toolUnderTest.Execute(context.Background(), tool.Input{
					Name: "batch",
					Arguments: map[string]any{
						"tools": []any{
							map[string]any{"name": "first"},
							map[string]any{"name": "second"},
						},
					},
				})
				done <- struct {
					result tool.Result
					err    error
				}{result: result, err: err}
			}()

			for range 2 {
				Eventually(started, 500*time.Millisecond).Should(Receive(),
					"expected both tools to start concurrently before either completes")
			}

			close(release)

			var outcome struct {
				result tool.Result
				err    error
			}
			Eventually(done, 500*time.Millisecond).Should(Receive(&outcome))
			Expect(outcome.err).NotTo(HaveOccurred())
			Expect(outcome.result.Error).NotTo(HaveOccurred())

			var payload []map[string]any
			Expect(json.Unmarshal([]byte(outcome.result.Output), &payload)).To(Succeed())
			Expect(payload).To(HaveLen(2))
		})

		It("preserves partial failures alongside successful outputs", func() {
			registry := tool.NewRegistry()
			registry.Register(fakeTool{name: "ok", output: "done"})
			registry.Register(fakeTool{name: "fail", err: errors.New("boom")})

			toolUnderTest := batch.New(registry)
			result, err := toolUnderTest.Execute(context.Background(), tool.Input{
				Name: "batch",
				Arguments: map[string]any{
					"tools": []any{
						map[string]any{"name": "ok"},
						map[string]any{"name": "fail"},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
			Expect(result.Output).NotTo(BeEmpty())
			Expect(fmt.Sprint(result.Error)).NotTo(BeEmpty())
		})
	})
})
