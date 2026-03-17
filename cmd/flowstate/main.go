package main

import (
	"fmt"
	"os"

	_ "github.com/anthropics/anthropic-sdk-go"
	_ "github.com/charmbracelet/bubbletea"
	_ "github.com/charmbracelet/lipgloss"
	_ "github.com/cucumber/godog"
	_ "github.com/go-chi/chi/v5"
	_ "github.com/mark3labs/mcp-go/mcp"
	_ "github.com/ollama/ollama/api"
	_ "github.com/openai/openai-go"
	_ "github.com/pkoukk/tiktoken-go"
	_ "github.com/spf13/cobra"
	_ "gopkg.in/yaml.v3"
)

func main() {
	fmt.Println("FlowState Agent Platform")
	os.Exit(0)
}
