package ollama_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/ollama"
)

// resolveOllamaContextLength specs cover the lookup table installed to
// close the failover.Manager.ResolveContextLength gap where every Ollama
// model previously fell through to the generic 4096 fallback. The
// failover manager asks the provider for models and reads ContextLength
// per model, so correct resolution here is the only way the
// auto-compactor can know when a 70% threshold of a 131k-token window
// has been crossed.
//
// The function under test is unexported; we drive it through the
// ResolveOllamaContextLengthForTest shim in export_test.go.
var _ = Describe("resolveOllamaContextLength", func() {
	DescribeTable("resolves model tags to the correct context length",
		func(in string, want int) {
			Expect(ollama.ResolveOllamaContextLengthForTest(in)).To(Equal(want))
		},
		Entry("llama 3.2 latest tag resolves to 131k", "llama3.2:latest", 131072),
		Entry("llama 3.2 size tag resolves to 131k", "llama3.2:3b", 131072),
		Entry("llama 3.1 resolves to 131k", "llama3.1:8b", 131072),
		Entry("llama 3 without dot resolves to 8k", "llama3:latest", 8192),
		Entry("qwen 2.5 instruct resolves to 32k", "qwen2.5:7b-instruct", 32768),
		Entry("qwen 2 resolves to 32k", "qwen2:7b", 32768),
		Entry("granite 4 resolves to 131k", "granite4:tiny", 131072),
		Entry("granite 3 resolves to 131k", "granite3:8b", 131072),
		Entry("mistral tag resolves to 32k", "mistral:latest", 32768),
		Entry("mixtral resolves to 32k", "mixtral:8x7b", 32768),
		Entry("phi 3 resolves to 131k", "phi3:mini", 131072),
		Entry("gemma 2 resolves to 8k", "gemma2:9b", 8192),
		Entry("codellama resolves to 16k", "codellama:7b", 16384),
		Entry("deepseek resolves to 16k", "deepseek:7b", 16384),
		Entry("case-insensitive prefix match", "LLaMA3.2:latest", 131072),
		Entry("empty string returns default", "", 4096),
		Entry("unknown family returns default", "some-custom-model:v1", 4096),
		Entry("longest-prefix wins over short prefix", "llama3.2:70b", 131072),
	)
})
