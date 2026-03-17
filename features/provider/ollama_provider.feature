Feature: Ollama Provider
  As a FlowState user
  I want to use local Ollama models
  So that I can run AI locally without cloud dependencies

  Background:
    Given the Ollama provider is configured

  @smoke
  Scenario: Provider returns its name
    When I request the provider name
    Then it should return "ollama"

  @smoke
  Scenario: List available models
    Given Ollama has models available
    When I request the list of models
    Then I should receive a list of models with context lengths

  Scenario: Send a chat request
    Given a valid chat request with messages
    When I send the chat request to the Ollama provider
    Then I should receive a chat response with a message

  Scenario: Stream a chat response
    Given a valid chat request with messages
    When I stream the chat request to the Ollama provider
    Then I should receive stream chunks until done

  Scenario: Generate embeddings
    Given text to embed
    When I request embeddings from the Ollama provider
    Then I should receive a vector of floats
