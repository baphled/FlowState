# FlowState Demo Walkthrough

This guide provides a step-by-step walkthrough of FlowState's core features. Follow these steps to experience the power of an AI-assisted terminal workflow.

## Prerequisites

- Go 1.22+ installed.
- Ollama running locally (`ollama serve`).
- At least one model pulled (e.g., `ollama pull llama3.2`).

## Scenario 1: Basic Chat and Markdown Rendering

In this scenario, we'll start a conversation and see how FlowState handles rich markdown.

1. **Launch FlowState**:
   ```bash
   flowstate chat
   ```

2. **Send a message**:
   - Type: `Explain the Model Context Protocol (MCP) using a markdown table for its components.`
   - Press `Enter` to send.

3. **Observe the response**:
   - The AI starts streaming the response immediately.
   - The response is rendered with **Glamour**, showing bold headers, tables, and code blocks with proper syntax highlighting.
   - Notice the **StatusBar** at the bottom showing `ollama` as the provider and `llama3.2` as the model.

## Scenario 2: MCP Tool Discovery

FlowState can use external tools via MCP. Let's see how the AI identifies available tools.

1. **Configure an MCP server**:
   Ensure your `~/.config/flowstate/config.yaml` includes a server like `filesystem`:
   ```yaml
   mcp_servers:
     - name: "filesystem"
       command: "npx"
       args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/docs"]
       enabled: true
   ```

2. **Ask about tools**:
   - Type: `What tools do you have access to through the filesystem MCP server?`
   - Press `Enter`.

3. **Verification**:
   - The AI will use the `ListTools` capability of the MCP client to discover tools like `read_file`, `write_file`, and `list_directory`.
   - It will list these tools in its response.

## Scenario 3: Token Monitoring and Status

1. **Monitor the StatusBar**:
   - While the AI is responding, look at the bottom right of the terminal.
   - You will see a real-time **Token Count** incrementing as the response streams in.
   - This helps you stay aware of model usage and context window limits.

## Scenario 4: Session Resume

1. **Quit FlowState**:
   - Press `Ctrl+C` to quit.

2. **Find your session**:
   - FlowState automatically saves sessions.
   - List recent sessions:
     ```bash
     flowstate session list
     ```
   - Copy the ID of your last session (e.g., `session-123456789`).

3. **Resume the session**:
   ```bash
   flowstate chat --session session-123456789
   ```
   - The conversation history is restored, and you can continue exactly where you left off.

## Scenario 5: List Available Models

1. **List models from all providers**:
   ```bash
   flowstate models
   ```

2. **Observe the output**:
   - Each line shows the provider, model ID, and context length.
   - Only providers with valid credentials or connections display their models.

3. **Use a specific model**:
   ```bash
   flowstate chat --model gpt-4o
   ```
   - The chat session will use the specified model instead of the default.

## Conclusion

FlowState combines the flexibility of LLMs with the power of terminal tools and local model control. For more advanced usage, check out the `flowstate help` command inside the TUI.
