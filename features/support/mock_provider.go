package support

import (
	"context"
	"strings"
)

// ChatRequest represents a chat completion request for BDD testing.
type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []Tool
}

// ChatResponse represents a chat completion response for BDD testing.
type ChatResponse struct {
	Message Message
	Usage   Usage
}

// StreamChunk represents a streaming response chunk for BDD testing.
type StreamChunk struct {
	Content   string
	Done      bool
	Error     error
	EventType string
	ToolCall  *ToolCall
}

// EmbedRequest represents an embedding request for BDD testing.
type EmbedRequest struct {
	Input string
	Model string
}

// Model represents a model definition for BDD testing.
type Model struct {
	ID            string
	Provider      string
	ContextLength int
}

// Usage represents token usage statistics for BDD testing.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Tool represents a tool definition for BDD testing.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]interface{}
}

// ToolCall represents a tool invocation for BDD testing.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

// MockProvider is a test double for provider implementations in BDD scenarios.
type MockProvider struct {
	name          string
	responses     []string
	embeddings    []float64
	models        []Model
	responseIndex int
}

// NewMockProvider creates a new MockProvider with default responses.
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

// Name returns the provider name.
func (m *MockProvider) Name() string {
	return m.name
}

// Stream streams a response for the given chat request.
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
	if name := findNameFromHistory(req.Messages); name != "" {
		return "Your name is " + name + "."
	}
	return m.responses[m.responseIndex%len(m.responses)]
}

func findNameFromHistory(messages []Message) string {
	if !containsNameQuery(messages) {
		return ""
	}
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		name := extractNameFromContent(msg.Content)
		if name != "" {
			return name
		}
	}
	return ""
}

func containsNameQuery(messages []Message) bool {
	for _, msg := range messages {
		if msg.Role == "user" && strings.Contains(strings.ToLower(msg.Content), "what is my name") {
			return true
		}
	}
	return false
}

func extractNameFromContent(content string) string {
	lowerContent := strings.ToLower(content)
	if !strings.Contains(lowerContent, "my name is") {
		return ""
	}
	idx := strings.Index(lowerContent, "my name is")
	nameStart := idx + len("my name is")
	if nameStart >= len(content) {
		return ""
	}
	return strings.TrimSpace(content[nameStart:])
}

// Chat performs a synchronous chat completion request.
func (m *MockProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	response := m.responses[m.responseIndex%len(m.responses)]
	m.responseIndex++
	return ChatResponse{
		Message: Message{Role: "assistant", Content: response},
		Usage:   Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}, nil
}

// Embed generates embeddings for the given input.
func (m *MockProvider) Embed(_ context.Context, _ EmbedRequest) ([]float64, error) {
	return m.embeddings, nil
}

// Models returns the available models.
func (m *MockProvider) Models() ([]Model, error) {
	return m.models, nil
}

// SetResponses configures the mock responses.
func (m *MockProvider) SetResponses(responses []string) {
	m.responses = responses
}

// SetEmbeddings configures the mock embeddings.
func (m *MockProvider) SetEmbeddings(embeddings []float64) {
	m.embeddings = embeddings
}

// SetModels configures the mock models.
func (m *MockProvider) SetModels(models []Model) {
	m.models = models
}
