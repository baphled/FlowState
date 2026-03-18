package ollama

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	ollamaAPI "github.com/ollama/ollama/api"
)

type Provider struct {
	client *ollamaAPI.Client
	host   string
}

func New(host string) (*Provider, error) {
	client, err := ollamaAPI.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama client: %w", err)
	}
	return &Provider{
		client: client,
		host:   host,
	}, nil
}

func (p *Provider) Name() string {
	return "ollama"
}

func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 16)

	messages := make([]ollamaAPI.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, ollamaAPI.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	go func() {
		defer close(ch)

		chatReq := &ollamaAPI.ChatRequest{
			Model:    req.Model,
			Messages: messages,
			Stream:   boolPtr(true),
		}

		err := p.client.Chat(ctx, chatReq, func(resp ollamaAPI.ChatResponse) error {
			chunk := provider.StreamChunk{
				Content: resp.Message.Content,
				Done:    resp.Done,
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ch <- chunk:
			}
			return nil
		})
		if err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
		}
	}()

	return ch, nil
}

func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	messages := make([]ollamaAPI.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, ollamaAPI.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	chatReq := &ollamaAPI.ChatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   boolPtr(false),
	}

	var finalResp ollamaAPI.ChatResponse
	err := p.client.Chat(ctx, chatReq, func(resp ollamaAPI.ChatResponse) error {
		finalResp = resp
		return nil
	})
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("ollama chat failed: %w", err)
	}

	return provider.ChatResponse{
		Message: provider.Message{
			Role:    finalResp.Message.Role,
			Content: finalResp.Message.Content,
		},
		Usage: provider.Usage{
			PromptTokens:     finalResp.PromptEvalCount,
			CompletionTokens: finalResp.EvalCount,
			TotalTokens:      finalResp.PromptEvalCount + finalResp.EvalCount,
		},
	}, nil
}

func (p *Provider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	embedReq := &ollamaAPI.EmbedRequest{
		Model: req.Model,
		Input: req.Input,
	}

	resp, err := p.client.Embed(ctx, embedReq)
	if err != nil {
		return nil, fmt.Errorf("ollama embed failed: %w", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, errors.New("no embeddings returned")
	}

	result := make([]float64, len(resp.Embeddings[0]))
	for i, v := range resp.Embeddings[0] {
		result[i] = float64(v)
	}

	return result, nil
}

func (p *Provider) Models() ([]provider.Model, error) {
	resp, err := p.client.List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("ollama list models failed: %w", err)
	}

	models := make([]provider.Model, 0, len(resp.Models))
	for i := range resp.Models {
		contextLen := 4096
		models = append(models, provider.Model{
			ID:            resp.Models[i].Name,
			Provider:      "ollama",
			ContextLength: contextLen,
		})
	}

	return models, nil
}

func boolPtr(b bool) *bool {
	return &b
}
