package provider

import "context"

type Message struct {
	Role    string
	Content string
}

type ChatRequest struct {
	Messages []Message
	Model    string
}

type ChatResponse struct {
	Content string
	Error   error
}

type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	Name() string
	Models() []string
}
