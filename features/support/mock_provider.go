package support

import (
	"context"
	"strings"
)

type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []Tool
}

type ChatResponse struct {
	Message Message
	Usage   Usage
}

type StreamChunk struct {
	Content   string
	Done      bool
	Error     error
	EventType string
	ToolCall  *ToolCall
}

type EmbedRequest struct {
	Input string
	Model string
}

type Model struct {
	ID            string
	Provider      string
	ContextLength int
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Tool struct {
	Name        string
	Description string
	Schema      map[string]interface{}
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

type MockProvider struct {
	name          string
	responses     []string
	embeddings    []float64
	models        []Model
	responseIndex int
}

func NewMockProvider() *MockProvider {
	return &MockProvider{
		name:       "mock",
		responses:  []string{"Hello! How can I help you today?"},
		embeddings: make([]float64, 384),
		models: []Model{
			{ID: "mock-model", Provider: "mock", ContextLength: 4096},
		},
	}
}

func (m *MockProvider) Name() string {
	return m.name
}

func (m *MockProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 16)
	go func() {
		defer close(ch)
		response := m.getContextualResponse(req)
		for i, r := range response {
			select {
			case <-ctx.Done():
				return
			case ch <- StreamChunk{Content: string(r), Done: i == len(response)-1}:
			}
		}
	}()
	return ch, nil
}

func (m *MockProvider) getContextualResponse(req ChatRequest) string {
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			if strings.Contains(strings.ToLower(msg.Content), "what is my name") {
				for _, prevMsg := range req.Messages {
					if prevMsg.Role == "user" {
						lowerContent := strings.ToLower(prevMsg.Content)
						if strings.Contains(lowerContent, "my name is") {
							idx := strings.Index(lowerContent, "my name is")
							nameStart := idx + len("my name is")
							if nameStart < len(prevMsg.Content) {
								name := strings.TrimSpace(prevMsg.Content[nameStart:])
								return "Your name is " + name + "."
							}
						}
					}
				}
			}
		}
	}
	return m.responses[m.responseIndex%len(m.responses)]
}

func (m *MockProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	response := m.responses[m.responseIndex%len(m.responses)]
	m.responseIndex++
	return ChatResponse{
		Message: Message{Role: "assistant", Content: response},
		Usage:   Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}, nil
}

func (m *MockProvider) Embed(ctx context.Context, req EmbedRequest) ([]float64, error) {
	return m.embeddings, nil
}

func (m *MockProvider) Models() ([]Model, error) {
	return m.models, nil
}

func (m *MockProvider) SetResponses(responses []string) {
	m.responses = responses
}

func (m *MockProvider) SetEmbeddings(embeddings []float64) {
	m.embeddings = embeddings
}

func (m *MockProvider) SetModels(models []Model) {
	m.models = models
}
