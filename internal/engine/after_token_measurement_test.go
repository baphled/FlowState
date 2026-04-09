package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/onsi/gomega"
)

type afterTokenEvidence struct {
	AfterSystemPromptTokens int     `json:"after_system_prompt_tokens"`
	AfterSkillHeaderTokens  int     `json:"after_skill_header_tokens"`
	AfterEstimatedTokens    int     `json:"after_estimated_tokens"`
	BaselineEstimatedTokens int     `json:"baseline_estimated_tokens"`
	ReductionPercent        float64 `json:"reduction_percent"`
}

func TestWriteAfterTokenMeasurementEvidence(t *testing.T) {
	t.Helper()

	assert := gomega.NewWithT(t)

	appCfg, err := config.LoadConfig()
	assert.Expect(err).NotTo(gomega.HaveOccurred())

	manifest := agent.Manifest{
		ID:   "after-token-measurement",
		Name: "After Token Measurement",
		Instructions: agent.Instructions{
			SystemPrompt: "You are a helpful assistant.",
		},
		Capabilities: agent.Capabilities{
			AlwaysActiveSkills: appCfg.AlwaysActiveSkills,
		},
	}

	eng := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "measurement-provider"},
		Manifest:     manifest,
		TokenCounter: ctxstore.NewTiktokenCounter(),
	})

	counter := ctxstore.NewTiktokenCounter()
	systemPrompt := eng.BuildSystemPrompt()
	systemPromptTokens := counter.Count(systemPrompt)

	skillCache := hook.NewSkillContentCache(appCfg.SkillDir)
	assert.Expect(skillCache.Init()).To(gomega.Succeed())

	autoloadCfg := hook.DefaultSkillAutoLoaderConfig()

	request := &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "measure after tokens with cache wired"},
		},
	}

	captured := request
	passthrough := func(_ context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		captured = req
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Done: true}
		close(ch)
		return ch, nil
	}

	hookFn := hook.SkillAutoLoaderHook(autoloadCfg, func() agent.Manifest { return manifest }, nil, skillCache)
	wrapped := hookFn(passthrough)
	_, err = wrapped(context.Background(), request)
	assert.Expect(err).NotTo(gomega.HaveOccurred())

	modifiedSystemTokens := 0
	if len(captured.Messages) > 0 && captured.Messages[0].Role == "system" {
		modifiedSystemTokens = counter.Count(captured.Messages[0].Content)
	}

	afterSkillHeaderTokens := modifiedSystemTokens - systemPromptTokens
	if afterSkillHeaderTokens < 0 {
		afterSkillHeaderTokens = 0
	}
	afterEstimatedTokens := modifiedSystemTokens

	baselinePath := filepath.Join("..", "..", ".sisyphus", "evidence", "task-1-baseline-tokens.json")
	baselineEstimated := 144879
	if baselineData, readErr := os.ReadFile(baselinePath); readErr == nil {
		var baseline baselineTokenEvidence
		if json.Unmarshal(baselineData, &baseline) == nil && baseline.EstimatedFirstTurnTokens > 0 {
			baselineEstimated = baseline.EstimatedFirstTurnTokens
		}
	}

	reductionPercent := float64(baselineEstimated-afterEstimatedTokens) / float64(baselineEstimated) * 100

	evidence := afterTokenEvidence{
		AfterSystemPromptTokens: systemPromptTokens,
		AfterSkillHeaderTokens:  afterSkillHeaderTokens,
		AfterEstimatedTokens:    afterEstimatedTokens,
		BaselineEstimatedTokens: baselineEstimated,
		ReductionPercent:        reductionPercent,
	}

	data, err := json.MarshalIndent(evidence, "", "  ")
	assert.Expect(err).NotTo(gomega.HaveOccurred())

	evidencePath := filepath.Join("..", "..", ".sisyphus", "evidence", "task-8-after-tokens.json")
	assert.Expect(os.MkdirAll(filepath.Dir(evidencePath), 0o755)).To(gomega.Succeed())
	assert.Expect(os.WriteFile(evidencePath, data, 0o600)).To(gomega.Succeed())
	_, err = os.Stat(evidencePath)
	assert.Expect(err).NotTo(gomega.HaveOccurred())
}
