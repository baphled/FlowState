## [2026-03-29] Task: Planning

### Architecture decisions
- FlowState hook system is request-middleware only (`Hook func(next HandlerFunc) HandlerFunc`) — new plugin hook types go in `internal/plugin/`, NOT `internal/hook/`
- MCP Manager (`internal/mcp/manager.go`) uses subprocess stdio + JSON-RPC — this is the direct precedent for external plugins
- Tool/Provider/Agent registries all follow same pattern: `sync.RWMutex` + `map[string]Type` — Plugin Registry must match exactly
- Config uses raw `yaml.v3` (NOT viper) despite docs saying viper — check `internal/config/config.go`

### Dropped scope
- model-tracker: deprecated in opencode 2026-02-12. FlowState has session enrichment already.
- model-context: env vars for opencode model-sync cron — FlowState has no such cron, models discovered dynamically
- ShellEnv hook type: removed — no plugin uses it. 4 hook types only: ChatParams, Event, ToolExecBefore, ToolExecAfter

### Key file locations
- Hook system: `internal/hook/hook.go` — DO NOT MODIFY
- Registry pattern: `internal/tool/registry.go:8-78` — PRIMARY reference for PluginRegistry
- MCP subprocess pattern: `internal/mcp/manager.go` — PRIMARY reference for external plugin spawner
- Config struct: `internal/config/config.go:17-29` — add PluginsConfig field here
- App startup: `internal/app/app.go` — wire plugins here
- Engine: `internal/engine/engine.go` — EventBus + event emissions go here

### Implementation notes
- `internal/plugin/` now owns the new hook contract surface for plugins and stays separate from request middleware in `internal/hook/`.
- Hook type identifiers are string-backed and limited to four values: chat.params, event, tool.execute.before, and tool.execute.after.
- The contract tests use Ginkgo with type reflection to lock the public API shape before any plugin wiring work begins.

### Config follow-up
- Plugin configuration is additive to `AppConfig`; defaults must be applied after YAML unmarshal to preserve backwards compatibility.
- The plugin directory default resolves to `~/.config/flowstate/plugins`, and timeout defaults to 5 seconds when omitted.
- Existing config fixtures continue to pass without a `plugins` key because the field uses `yaml:"plugins,omitempty"`.
