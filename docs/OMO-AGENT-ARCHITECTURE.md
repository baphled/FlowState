# OMO Agent Manifest & System Prompt Architecture

**Investigation Date:** 2026-03-27  
**Source:** code-yeongyu/oh-my-openagent (43K stars, TypeScript)  
**Status:** Complete analysis of agent storage, manifest format, and prompt integration

---

## Executive Summary

OMO takes a **code-first, dynamic generation** approach to agent manifests and system prompts:

- **Agent definitions:** TypeScript factory functions (NOT JSON/YAML files)
- **System prompts:** Generated dynamically at runtime from agent metadata + available tools/skills
- **Skill integration:** YAML frontmatter in SKILL.md files, injected into prompts at instantiation
- **Configuration:** JSONC config file for overrides (model, variant, prompt_append)
- **User agents:** NOT YET IMPLEMENTED (only built-in agents available)

This is fundamentally different from FlowState's current JSON manifest approach.

---

## 1. Where OMO Stores Agent Definitions

### Built-in Agents (Code-First)

**Location:** `src/agents/` directory  
**Format:** TypeScript factory functions  
**Pattern:**

```typescript
// src/agents/sisyphus.ts
const createSisyphusAgent: AgentFactory = (model: string) => ({
  description: "Powerful AI orchestrator...",
  mode: "all",
  model,
  maxTokens: 64000,
  prompt: buildDynamicSisyphusPrompt(...),
  color: "#00CED1",
  permission: { question: "allow", call_omo_agent: "deny" },
  reasoningEffort: "medium",
})
createSisyphusAgent.mode = "all"
```

**Key Points:**
- Agent is a **function** that takes a model string and returns an `AgentConfig`
- Static `mode` property exposed for pre-instantiation access
- No separate manifest file — everything in code
- 11 built-in agents defined this way (Sisyphus, Hephaestus, Oracle, Librarian, Explore, Multimodal-Looker, Metis, Momus, Atlas, Prometheus, Sisyphus-Junior)

### Agent Configuration Overrides

**Location:** `~/.config/oh-my-opencode/oh-my-openagent.jsonc`  
**Format:** JSONC (JSON with comments)  
**Example:**

```jsonc
{
  "agents": {
    "sisyphus": {
      "model": "anthropic/claude-opus-4-6",
      "ultrawork": { "model": "anthropic/claude-opus-4-6", "variant": "max" },
    },
    "hephaestus": {
      "model": "openai/gpt-5.4",
      "prompt_append": "Explore thoroughly, then implement. Prefer small, testable changes.",
    },
    "oracle": { "model": "openai/gpt-5.4", "variant": "high" },
  },
  "categories": {
    "quick": { "model": "opencode/gpt-5-nano" },
    "ultrabrain": { "model": "openai/gpt-5.4", "variant": "xhigh" },
  },
}
```

**Supported Override Fields:**
- `model` — Override default model
- `variant` — Model variant (max, high, xhigh, medium, etc.)
- `prompt_append` — Append text to agent prompt without code changes
- `ultrawork` — Special config for ultrawork mode (Sisyphus only)

### User Agents (Not Yet Implemented)

**Planned Location:** `~/.config/oh-my-opencode/agents/` (directory scanning)  
**Status:** Feature not yet implemented in OMO  
**Expected Format:** Would likely follow same factory pattern or JSON config

---

## 2. Agent Metadata & Frontmatter

### Agent Metadata for Prompt Generation

OMO uses `AgentPromptMetadata` interface to drive dynamic prompt generation:

```typescript
export interface AgentPromptMetadata {
  category: "exploration" | "specialist" | "advisor" | "utility"
  cost: "FREE" | "CHEAP" | "EXPENSIVE"
  triggers: DelegationTrigger[]  // { domain, trigger }
  useWhen?: string[]
  avoidWhen?: string[]
  dedicatedSection?: string  // Custom markdown section
  promptAlias?: string  // Display name (e.g., "Oracle" vs "oracle")
  keyTrigger?: string  // Phase 0 trigger
}
```

**Example (Sisyphus):**

```typescript
export const SISYPHUS_PROMPT_METADATA: AgentPromptMetadata = {
  category: "utility",
  cost: "EXPENSIVE",
  promptAlias: "Sisyphus",
  triggers: [],
}
```

**How It's Used:**
- Metadata drives dynamic prompt section generation
- Sisyphus prompt includes: Key Triggers, Tool Selection Table, Delegation Table, Category Skills Guide, Oracle Section, Hard Blocks, Anti-Patterns, Parallel Delegation
- Each section built from available agents' metadata
- Allows adding/removing agents without manually updating Sisyphus prompt

### NO Frontmatter in Agent Definitions

**Critical Finding:** Agent definitions do NOT use frontmatter.  
Frontmatter is ONLY used in skill definitions (SKILL.md files).

---

## 3. How OMO Loads System Prompts

### Dynamic Generation at Runtime

System prompts are **NOT stored in files**. They are **generated dynamically** when an agent is instantiated.

**Process:**

1. **Agent factory called** with model string
2. **Prompt builder function invoked** (e.g., `buildDynamicSisyphusPrompt()`)
3. **Builder composes sections** from:
   - Available agents (via metadata)
   - Available tools
   - Available skills
   - Available categories
4. **Model-specific variants applied** (GPT-5.4 vs Gemini vs Claude)
5. **Skills injected** at instantiation time
6. **Config overrides applied** (prompt_append)

**Code Example:**

```typescript
// src/agents/sisyphus.ts
function buildDynamicSisyphusPrompt(
  model: string,
  availableAgents: AvailableAgent[],
  availableTools: AvailableTool[] = [],
  availableSkills: AvailableSkill[] = [],
  availableCategories: AvailableCategory[] = [],
  useTaskSystem = false,
): string {
  const keyTriggers = buildKeyTriggersSection(availableAgents, availableSkills)
  const toolSelection = buildToolSelectionTable(availableAgents, availableTools, availableSkills)
  const delegationTable = buildDelegationTable(availableAgents)
  const oracleSection = buildOracleSection(availableAgents)
  // ... 10+ more sections
  
  return `<Role>
You are "Sisyphus" - Powerful AI Agent...
${keyTriggers}
${toolSelection}
${delegationTable}
...
`
}
```

### Skill Injection Pattern

Skills are loaded from SKILL.md files and injected into agent prompts:

```typescript
// src/agents/agent-builder.ts
if (agentWithCategory.skills?.length) {
  const { resolved } = resolveMultipleSkills(agentWithCategory.skills, ...)
  if (resolved.size > 0) {
    const skillContent = Array.from(resolved.values()).join("\n\n")
    base.prompt = skillContent + (base.prompt ? "\n\n" + base.prompt : "")
  }
}
```

**Result:** Skills are prepended to agent prompt at instantiation time.

### Model-Specific Prompt Variants

Different models get different prompt sections:

```typescript
// src/agents/sisyphus.ts
if (isGpt5_4Model(model)) {
  const prompt = buildGpt54SisyphusPrompt(...)
  return { ...base, prompt }
}

if (isGeminiModel(model)) {
  // Apply Gemini-specific sections
  const prompt = buildDynamicSisyphusPrompt(...)
  // + buildGeminiToolMandate()
  // + buildGeminiDelegationOverride()
  // + buildGeminiVerificationOverride()
  return { ...base, prompt }
}
```

---

## 4. Skill Frontmatter Schema

### SKILL.md Frontmatter Format

Skills use YAML frontmatter between `---` delimiters:

```markdown
---
name: git-master
description: "MUST USE for ANY git operations. Atomic commits, rebase/squash, history search."
model: anthropic/claude-opus-4-6
agent: sisyphus
allowed-tools: ["bash", "read"]
license: MIT
compatibility: "OpenCode 2.0+"
metadata:
  category: "git"
  tier: "essential"
mcp:
  servers:
    - name: git-master
      command: git-master
---

# Git Master Agent

You are a Git expert combining three specializations...
```

### SkillMetadata Interface

```typescript
export interface SkillMetadata {
  name?: string                    // Skill identifier (defaults to filename)
  description?: string             // One-line purpose
  model?: string                   // Override model for this skill
  "argument-hint"?: string         // Hint for argument format
  agent?: string                   // Which agent should load this skill
  subtask?: boolean                // Whether skill is a subtask
  license?: string                 // Skill license
  compatibility?: string           // Compatibility notes
  metadata?: Record<string, string> // Arbitrary key-value metadata
  "allowed-tools"?: string | string[] // Tools this skill can use
  mcp?: SkillMcpConfig             // MCP server configuration
}
```

### Frontmatter Parsing

```typescript
// src/shared/frontmatter.ts
export function parseFrontmatter<T = Record<string, unknown>>(
  content: string
): FrontmatterResult<T> {
  const frontmatterRegex = /^---\r?\n([\s\S]*?)\r?\n?---\r?\n([\s\S]*)$/
  const match = content.match(frontmatterRegex)
  
  if (!match) {
    return { data: {} as T, body: content, hadFrontmatter: false, parseError: false }
  }
  
  const yamlContent = match[1]
  const body = match[2]
  
  try {
    const parsed = yaml.load(yamlContent, { schema: yaml.JSON_SCHEMA })
    return { data: parsed as T, body, hadFrontmatter: true, parseError: false }
  } catch {
    return { data: {} as T, body, hadFrontmatter: true, parseError: true }
  }
}
```

**Key Points:**
- Uses regex to extract frontmatter block
- Parses YAML with `js-yaml` library
- Uses `JSON_SCHEMA` for security (prevents code execution)
- Returns: `{ data, body, hadFrontmatter, parseError }`

### Skill Loading Process

```typescript
// src/features/opencode-skill-loader/loaded-skill-from-path.ts
export async function loadSkillFromPath(options: {
  skillPath: string
  resolvedPath: string
  defaultName: string
  scope: SkillScope
  namePrefix?: string
}): Promise<LoadedSkill | null> {
  const content = await fs.readFile(options.skillPath, "utf-8")
  const { data, body } = parseFrontmatter<SkillMetadata>(content)
  
  const skillName = data.name || options.defaultName
  const resolvedBody = resolveSkillPathReferences(body.trim(), options.resolvedPath)
  const templateContent = `<skill-instruction>
Base directory for this skill: ${options.resolvedPath}/
File references (@path) in this skill are relative to this directory.

${resolvedBody}
</skill-instruction>

<user-request>
$ARGUMENTS
</user-request>`
  
  return {
    name: skillName,
    path: options.skillPath,
    definition: { name: skillName, description: data.description, ... },
    scope: options.scope,
    metadata: data.metadata,
    allowedTools: parseAllowedTools(data["allowed-tools"]),
    mcpConfig: data.mcp,
    lazyContent: { loaded: true, content: templateContent, ... },
  }
}
```

### Skill Discovery Locations

Skills are loaded from multiple locations in order:

1. **Builtin:** `src/features/builtin-skills/` (git-master, frontend-ui-ux, dev-browser, agent-browser)
2. **Config:** `~/.config/oh-my-opencode/skills/`
3. **User:** `~/.local/share/oh-my-opencode/skills/`
4. **Project:** `./skills/` (in project root)

**Search Pattern:**
- For each directory: look for `SKILL.md` or `{dirname}.md`
- Recursive search up to depth 2
- Duplicate names skipped (first found wins)

---

## 5. Built-in vs User Agents

### Built-in Agents (11 Total)

| Agent | Mode | Purpose | Default Model |
|-------|------|---------|---|
| **Sisyphus** | all | Main orchestrator, plans + delegates | claude-opus-4-6 |
| **Hephaestus** | all | Autonomous deep worker | gpt-5.4 |
| **Oracle** | subagent | Read-only architecture consultant | gpt-5.4 |
| **Librarian** | subagent | External docs/code search | minimax-m2.7 |
| **Explore** | subagent | Fast codebase grep | grok-code-fast-1 |
| **Multimodal-Looker** | subagent | PDF/image analysis | gpt-5.3-codex |
| **Metis** | subagent | Pre-planning consultant | claude-opus-4-6 |
| **Momus** | subagent | Plan reviewer | gpt-5.4 |
| **Atlas** | primary | Todo-list orchestrator | claude-sonnet-4-6 |
| **Prometheus** | — | Strategic planner (internal) | claude-opus-4-6 |
| **Sisyphus-Junior** | all | Category-spawned executor | claude-sonnet-4-6 |

**Agent Modes:**
- `primary` — Respects UI-selected model, uses fallback chain
- `subagent` — Uses own fallback chain, ignores UI selection
- `all` — Available in both contexts

### User Agents (Not Yet Implemented)

**Status:** Feature planned but not yet implemented in OMO.

**Expected Pattern:**
- Directory: `~/.config/oh-my-opencode/agents/`
- Format: Likely JSON or JSONC (not TypeScript)
- Discovery: Directory scanning at startup
- Merging: User agents + built-in agents + config overrides

**Current Limitation:** Only built-in agents available. User agents would require:
1. New agent loader (similar to skill loader)
2. Agent registry update
3. Manifest validation
4. Prompt generation for user agents

---

## 6. Agent Config Structure

### AgentConfig Interface

```typescript
export interface AgentConfig {
  description: string
  mode: AgentMode  // "primary" | "subagent" | "all"
  model: string
  maxTokens?: number
  prompt: string  // System prompt (single string, not separate system/user)
  color?: string
  permission?: {
    question?: "allow" | "deny"
    call_omo_agent?: "allow" | "deny"
    // ... other tool permissions
  }
  reasoningEffort?: "low" | "medium" | "high"
  thinking?: { type: "enabled", budgetTokens: number }
  temperature?: number
  variant?: string  // Model variant (max, high, xhigh, etc.)
}
```

**Key Points:**
- `prompt` is a single string field (not separate system_prompt/user_prompt)
- `mode` controls availability context
- `permission` restricts tool access per agent
- `variant` allows model-specific tuning (e.g., claude-opus-4-6 max vs medium)

---

## 7. Patterns for Converting Manifest to Prompt

### Pattern 1: Metadata-Driven Sections

Agent metadata drives prompt section generation:

```typescript
// Available agents passed to prompt builder
const agents: AvailableAgent[] = [
  { name: "oracle", metadata: ORACLE_PROMPT_METADATA, ... },
  { name: "librarian", metadata: LIBRARIAN_PROMPT_METADATA, ... },
  // ...
]

// Prompt builder uses metadata to generate sections
const delegationTable = buildDelegationTable(agents)
// Generates markdown table from agents' triggers
```

### Pattern 2: Skill Injection

Skills are prepended to agent prompt:

```typescript
// At agent instantiation
const skillContent = loadedSkills.map(s => s.content).join("\n\n")
const finalPrompt = skillContent + "\n\n" + basePrompt
```

### Pattern 3: Config Overrides

Runtime config allows prompt modification without code changes:

```jsonc
{
  "agents": {
    "sisyphus": {
      "prompt_append": "Always verify before shipping. No shortcuts."
    }
  }
}
```

### Pattern 4: Model-Specific Variants

Different models get different prompt sections:

```typescript
if (isGpt5_4Model(model)) {
  return buildGpt54SisyphusPrompt(...)
} else if (isGeminiModel(model)) {
  return buildDynamicSisyphusPrompt(...) + buildGeminiToolMandate()
} else {
  return buildDynamicSisyphusPrompt(...)
}
```

---

## 8. Key Differences from FlowState

| Aspect | OMO | FlowState (Current) |
|--------|-----|---|
| **Agent Storage** | TypeScript factories | JSON manifests |
| **System Prompts** | Generated dynamically | Embedded in JSON |
| **Frontmatter** | Skills only (SKILL.md) | Agents (agent.json) |
| **User Agents** | Not yet implemented | Supported (agents/) |
| **Prompt Composition** | Additive (base + sections + skills) | Static (single prompt) |
| **Config Overrides** | JSONC (model, variant, prompt_append) | JSON (model_preferences) |
| **Skill Integration** | Injected at instantiation | Loaded separately |
| **Metadata** | AgentPromptMetadata (category, cost, triggers) | Delegation table in manifest |

---

## 9. Recommendations for FlowState

### Option A: Adopt OMO Pattern (Recommended)

**Pros:**
- Dynamic prompt generation scales better
- Metadata-driven sections easier to maintain
- Skill injection cleaner
- Model-specific variants built-in

**Cons:**
- Requires TypeScript/code-based agents
- More complex than JSON
- Harder for non-developers to customize

**Implementation:**
1. Convert agent JSON to TypeScript factories
2. Extract prompt into builder functions
3. Add AgentPromptMetadata to each agent
4. Implement skill injection at instantiation
5. Support config overrides (prompt_append)

### Option B: Hybrid Approach (Pragmatic)

**Keep JSON manifests, add frontmatter:**

```json
{
  "id": "planner",
  "name": "Planner",
  "description": "Strategic planning agent",
  "mode": "primary",
  "model_preferences": { ... },
  "frontmatter": {
    "category": "specialist",
    "cost": "EXPENSIVE",
    "triggers": [
      { "domain": "Planning", "trigger": "Multi-day projects" }
    ]
  },
  "system_prompt": "..."
}
```

**Pros:**
- Keeps JSON structure
- Adds metadata for future prompt generation
- Easier migration path
- Non-developers can still edit

**Cons:**
- Doesn't solve dynamic generation
- Still requires code changes for prompt updates

### Option C: Markdown Frontmatter (Minimal Change)

**Store agent definitions as markdown with frontmatter:**

```markdown
---
id: planner
name: Planner
description: Strategic planning agent
mode: primary
model_preferences:
  complexity:
    high: claude-opus-4-6
    medium: claude-sonnet-4-6
category: specialist
cost: EXPENSIVE
triggers:
  - domain: Planning
    trigger: Multi-day projects
---

# Planner Agent

You are a strategic planning agent...
```

**Pros:**
- Readable format
- Frontmatter for metadata
- Prompt content in same file
- Similar to OMO skill pattern

**Cons:**
- Requires markdown parser
- Less structured than JSON
- Harder to validate

---

## 10. Implementation Checklist for FlowState

If adopting OMO patterns:

- [ ] Define `AgentPromptMetadata` interface
- [ ] Extract prompts into builder functions
- [ ] Implement metadata-driven section generation
- [ ] Add skill injection at agent instantiation
- [ ] Support `prompt_append` in config
- [ ] Implement model-specific prompt variants
- [ ] Add user agent directory scanning (future)
- [ ] Create agent factory pattern
- [ ] Update agent discovery/registry
- [ ] Add frontmatter parsing for skills
- [ ] Document agent creation process

---

## References

**OMO Repository:** https://github.com/code-yeongyu/oh-my-openagent  
**Key Files:**
- Agent definitions: `src/agents/*.ts`
- Prompt builders: `src/agents/dynamic-agent-prompt-builder.ts`
- Skill loader: `src/features/opencode-skill-loader/`
- Frontmatter parser: `src/shared/frontmatter.ts`
- Config schema: `src/config/schema/oh-my-opencode-config.ts`
- Example config: `docs/examples/default.jsonc`

