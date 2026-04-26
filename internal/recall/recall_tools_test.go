package recall_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	recall "github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("RecallToolFactory", func() {
	It("registers all recall tools (search_context, get_messages, summarize_context)", func() {
		cfg := &engine.Config{}
		cfg.Store = recall.NewEmptyContextStore("test-model")
		cfg.EmbeddingProvider = stubProvider{}
		cfg.TokenCounter = stubTokenCounter{}
		cfg.Manifest.ContextManagement.EmbeddingModel = "test-model"

		registered := recall.RegisterRecallTools(cfg)

		expectedTools := []string{
			"search_context",
			"get_messages",
			"summarize_context",
		}

		Expect(registered).To(HaveLen(len(expectedTools)))
		Expect(cfg.Tools).To(HaveLen(len(expectedTools)))

		for _, name := range expectedTools {
			found := false
			for _, registeredTool := range cfg.Tools {
				if registeredTool.Name() == name {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "Tool %s should be registered in engine config", name)
		}
	})
})

type stubProvider struct{}

var errStubProvider = errors.New("stub provider error")

func (stubProvider) Name() string { return "stub" }

func (stubProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errStubProvider
}

func (stubProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errStubProvider
}

func (stubProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{1}, errStubProvider
}

func (stubProvider) Models() ([]provider.Model, error) { return nil, errStubProvider }

type stubTokenCounter struct{}

func (stubTokenCounter) Count(string) int { return 1 }

func (stubTokenCounter) ModelLimit(string) int { return 1 }

var _ provider.Provider = stubProvider{}
var _ ctxstore.TokenCounter = stubTokenCounter{}
var _ tool.Tool = (*recall.SearchContextTool)(nil)
