package openai

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

type Provider struct {
	client openaiAPI.Client
}

func New(apiKey string) (*Provider, error) {
	if apiKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}
	client := openaiAPI.NewClient(option.WithAPIKey(apiKey))
	return &Provider{
		client: client,
	}, nil
}

func (p *Provider) Name() string {
	return "openai"
}

func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 16)

	messages := make([]openaiAPI.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			messages = append(messages, openaiAPI.UserMessage(m.Content))
		case "assistant":
			messages = append(messages, openaiAPI.AssistantMessage(m.Content))
		case "system":
			messages = append(messages, openaiAPI.SystemMessage(m.Content))
		}
	}

	go func() {
		defer close(ch)

		stream := p.client.Chat.Completions.NewStreaming(ctx, openaiAPI.ChatCompletionNewParams{
			Model:    req.Model,
			Messages: messages,
		})

		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta
				ch <- provider.StreamChunk{
					Content: delta.Content,
					Done:    chunk.Choices[0].FinishReason != "",
				}
			}
		}

		if err := stream.Err(); err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
		}
	}()

	return ch, nil
}

func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	messages := make([]openaiAPI.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			messages = append(messages, openaiAPI.UserMessage(m.Content))
		case "assistant":
			messages = append(messages, openaiAPI.AssistantMessage(m.Content))
		case "system":
			messages = append(messages, openaiAPI.SystemMessage(m.Content))
		}
	}

	resp, err := p.client.Chat.Completions.New(ctx, openaiAPI.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: messages,
	})
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("openai chat failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return provider.ChatResponse{}, errors.New("no choices in response")
	}

	return provider.ChatResponse{
		Message: provider.Message{
			Role:    string(resp.Choices[0].Message.Role),
			Content: resp.Choices[0].Message.Content,
		},
		Usage: provider.Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
		},
	}, nil
}

func (p *Provider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	model := req.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	resp, err := p.client.Embeddings.New(ctx, openaiAPI.EmbeddingNewParams{
		Model: model,
		Input: openaiAPI.EmbeddingNewParamsInputUnion{
			OfString: openaiAPI.String(req.Input),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed failed: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, errors.New("no embeddings returned")
	}

	return resp.Data[0].Embedding, nil
}

func (p *Provider) Models() ([]provider.Model, error) {
	return []provider.Model{
		{ID: "gpt-4o", Provider: "openai", ContextLength: 128000},
		{ID: "gpt-4o-mini", Provider: "openai", ContextLength: 128000},
		{ID: "gpt-4-turbo", Provider: "openai", ContextLength: 128000},
		{ID: "gpt-3.5-turbo", Provider: "openai", ContextLength: 16385},
	}, nil
}
