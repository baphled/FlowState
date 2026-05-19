@demo-install
Feature: Demo quickstart installation
  As a non-specialist NPR demo user
  I want one documented local bootstrap path
  So that I can run FlowState with memory and recall enabled

  Scenario: Demo quickstart assets are available
    Given the FlowState repository root is available
    Then the demo quickstart document should mention:
      | text                     |
      | Who this is for          |
      | What this installs       |
      | Prerequisites            |
      | Quickstart               |
      | What Docker is used for  |
      | What remains native      |
      | Environment variables    |
      | Running Qdrant           |
      | Running FlowState service |
      | Running the web UI       |
      | Troubleshooting          |
      | Known limitations        |
    And the demo environment example should mention:
      | text              |
      | ANTHROPIC_API_KEY |
      | QDRANT_URL        |
      | QDRANT_COLLECTION |
      | OLLAMA_HOST       |
    And the demo bootstrap script should be executable
    And the demo bootstrap script should mention:
      | text                                           |
      | docker compose -f docker-compose.dev.yml up -d qdrant |
      | ollama pull nomic-embed-text                  |
      | flowstate memory-tools install                |
      | flowstate auth status                         |
      | flowstate swarm list                          |
      | flowstate agents list                         |
    And the demo Makefile shortcuts should be available:
      | target         |
      | demo-bootstrap |
      | demo-qdrant-up |
      | demo-check     |
      | demo-run       |
