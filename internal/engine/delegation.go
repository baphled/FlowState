package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

var (
	errDelegationNotAllowed     = errors.New("delegation not allowed for this agent")
	errRoutingFieldRequired     = errors.New("category or subagent_type must be provided")
	errMessageMustBeString      = errors.New("message must be a string")
	errCategoryMustBeString     = errors.New("category must be a string")
	errSubagentTypeMustBeString = errors.New("subagent_type must be a string")
	errSessionIDMustBeString    = errors.New("session_id must be a string")
	errLoadSkillsMustBeArray    = errors.New("load_skills must be an array of strings")
	errHandoffMustBeObject      = errors.New("handoff must be an object")
	errBackgroundModeDisabled   = errors.New("background mode disabled: no background manager configured")
	errCircuitBreakerOpen       = errors.New("circuit breaker open: too many delegation failures")
	errDepthLimitExceeded       = errors.New("depth limit exceeded: maximum delegation depth reached")
	errBudgetLimitExceeded      = errors.New("budget limit exceeded: maximum concurrent delegations reached")
	errAgentNotInAllowlist      = errors.New("agent not in delegation allowlist")
)

const maxDelegationFailures = 3

// streamOutputKeyType identifies the context key used for streaming output.
type streamOutputKeyType struct{}

var streamOutputKey streamOutputKeyType

// WithStreamOutput returns a child context carrying the given output channel
// so that tools (e.g. DelegateTool) can inject chunks into the parent stream.
//
// Expected:
//   - ctx is a valid context to extend.
//   - ch is the stream output channel to attach.
//
// Returns:
//   - A child context containing the output channel.
//
// Side effects:
//   - Stores the output channel in the returned context for later retrieval.
func WithStreamOutput(ctx context.Context, ch chan<- provider.StreamChunk) context.Context {
	return context.WithValue(ctx, streamOutputKey, ch)
}

// streamOutputFromContext extracts the output channel from the context, if present.
//
// Expected:
//   - ctx may carry a stream output channel stored by WithStreamOutput.
//
// Returns:
//   - The output channel and true when present, or a nil channel and false otherwise.
//
// Side effects:
//   - None.
func streamOutputFromContext(ctx context.Context) (chan<- provider.StreamChunk, bool) {
	ch, ok := ctx.Value(streamOutputKey).(chan<- provider.StreamChunk)
	return ch, ok
}

// DelegateTool enables an engine to delegate tasks to other agents.
type DelegateTool struct {
	engines            map[string]*Engine
	delegation         agent.Delegation
	sourceAgentID      string
	backgroundManager  *BackgroundTaskManager
	coordinationStore  coordination.Store
	embeddingDiscovery *discovery.EmbeddingDiscovery
	circuitBreaker     *delegation.CircuitBreaker
	spawnLimits        delegation.SpawnLimits
	skillResolver      SkillResolver
	categoryResolver   *CategoryResolver
	registry           *agent.Registry
}

// delegationTarget carries the resolved agent, engine, and message for delegation.
type delegationTarget struct {
	agentID          string
	engine           *Engine
	message          string
	handoff          *delegation.Handoff
	chainID          string
	resolvedModel    string
	resolvedProvider string
}

// delegationParams groups the parsed delegation input fields.
type delegationParams struct {
	category     string
	subagentType string
	message      string
	loadSkills   []string
	sessionID    string
	handoff      *delegation.Handoff
	runAsync     bool
}

// delegationResult carries the aggregated response and stream metadata from delegation.
type delegationResult struct {
	response  string
	toolCalls int
	lastTool  string
}

// NewDelegateTool creates a new delegation tool for the given engines, delegation configuration,
// and source agent identifier used for event attribution.
//
// Expected:
//   - engines is a map of agent IDs to their Engine instances.
//   - delegation is the delegation configuration for the current agent.
//   - sourceAgentID identifies the agent that owns this tool.
//
// Returns:
//   - A configured DelegateTool instance.
//
// Side effects:
//   - None.
func NewDelegateTool(engines map[string]*Engine, delegationConfig agent.Delegation, sourceAgentID string) *DelegateTool {
	return &DelegateTool{
		engines:        engines,
		delegation:     delegationConfig,
		sourceAgentID:  sourceAgentID,
		circuitBreaker: delegation.NewCircuitBreaker(maxDelegationFailures),
		spawnLimits:    delegation.DefaultSpawnLimits(),
	}
}

// NewDelegateToolWithBackground creates a new delegation tool with background task support.
//
// Expected:
//   - engines is a map of agent IDs to their Engine instances.
//   - delegation is the delegation configuration for the current agent.
//   - sourceAgentID identifies the agent that owns this tool.
//   - backgroundManager is the manager for tracking background tasks.
//   - coordinationStore is the shared store for cross-agent coordination.
//
// Returns:
//   - A configured DelegateTool instance with background support.
//
// Side effects:
//   - None.
func NewDelegateToolWithBackground(
	engines map[string]*Engine,
	delegationConfig agent.Delegation,
	sourceAgentID string,
	backgroundManager *BackgroundTaskManager,
	coordinationStore coordination.Store,
) *DelegateTool {
	return &DelegateTool{
		engines:           engines,
		delegation:        delegationConfig,
		sourceAgentID:     sourceAgentID,
		backgroundManager: backgroundManager,
		coordinationStore: coordinationStore,
		circuitBreaker:    delegation.NewCircuitBreaker(maxDelegationFailures),
		spawnLimits:       delegation.DefaultSpawnLimits(),
	}
}

// SetEmbeddingDiscovery sets the embedding-based discovery for agent matching.
//
// Expected:
//   - ed is a non-nil EmbeddingDiscovery instance.
//
// Side effects:
//   - Sets the embeddingDiscovery field for use in target resolution.
func (d *DelegateTool) SetEmbeddingDiscovery(ed *discovery.EmbeddingDiscovery) {
	d.embeddingDiscovery = ed
}

// WithSpawnLimits configures spawn limits for delegation depth and budget enforcement.
//
// Expected:
//   - limits is a valid SpawnLimits configuration.
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Sets the spawnLimits field to enforce during Execute().
func (d *DelegateTool) WithSpawnLimits(limits delegation.SpawnLimits) *DelegateTool {
	d.spawnLimits = limits
	return d
}

// WithSkillResolver sets the skill resolver for injecting skills into child engine system prompts.
//
// Expected:
//   - r is a non-nil SkillResolver instance.
//
// Returns:
//   - The receiver for method chaining.
//
// Side effects:
//   - Sets the skillResolver field for skill injection during delegation.
func (d *DelegateTool) WithSkillResolver(r SkillResolver) *DelegateTool {
	d.skillResolver = r
	return d
}

// WithCategoryResolver sets the CategoryResolver used to map category names to model config.
//
// Expected:
//   - r is a non-nil CategoryResolver.
//
// Returns:
//   - The DelegateTool for method chaining.
//
// Side effects:
//   - Replaces any previously configured category resolver.
func (d *DelegateTool) WithCategoryResolver(r *CategoryResolver) *DelegateTool {
	d.categoryResolver = r
	return d
}

// WithRegistry sets the agent registry used for name and alias resolution.
//
// Expected:
//   - reg is a non-nil agent Registry.
//
// Returns:
//   - The DelegateTool for method chaining.
//
// Side effects:
//   - Replaces any previously configured registry.
func (d *DelegateTool) WithRegistry(reg *agent.Registry) *DelegateTool {
	d.registry = reg
	return d
}

// ResolveByNameOrAlias looks up an agent ID using the registry by name or alias.
//
// Expected:
//   - name is a non-empty string identifying an agent.
//
// Returns:
//   - The resolved agent ID and nil on success.
//   - Empty string and error if not found.
//
// Side effects:
//   - None.
func (d *DelegateTool) ResolveByNameOrAlias(name string) (string, error) {
	if d.registry == nil {
		return "", fmt.Errorf("no registry configured for agent %q lookup", name)
	}
	manifest, ok := d.registry.GetByNameOrAlias(name)
	if !ok {
		ids := make([]string, 0, len(d.registry.List()))
		for _, m := range d.registry.List() {
			ids = append(ids, m.ID)
		}
		return "", fmt.Errorf("unknown agent %q; available agents: %s", name, strings.Join(ids, ", "))
	}
	return manifest.ID, nil
}

// checkSpawnLimits validates that delegation respects configured depth and budget limits.
//
// Expected:
//   - handoff may be nil or contain depth metadata.
//
// Returns:
//   - An error if depth or budget limits are exceeded, nil otherwise.
//
// Side effects:
//   - None.
func (d *DelegateTool) checkSpawnLimits(handoff *delegation.Handoff) error {
	depth := 0
	if handoff != nil && handoff.Metadata != nil {
		if depthStr, ok := handoff.Metadata["depth"]; ok {
			var depthVal int
			if _, err := fmt.Sscanf(depthStr, "%d", &depthVal); err == nil {
				depth = depthVal
			}
		}
	}

	if d.spawnLimits.ExceedsDepth(depth) {
		return errDepthLimitExceeded
	}

	if d.backgroundManager != nil {
		if d.spawnLimits.ExceedsBudget(d.backgroundManager.ActiveCount()) {
			return errBudgetLimitExceeded
		}
	}

	return nil
}

// Name returns the tool name.
//
// Returns:
//   - The string "delegate".
//
// Side effects:
//   - None.
func (d *DelegateTool) Name() string {
	return "delegate"
}

// Description returns a human-readable description of the delegation tool.
//
// Returns:
//   - A string describing what the tool does.
//
// Side effects:
//   - None.
func (d *DelegateTool) Description() string {
	return "Delegate a task to another agent based on task type"
}

// Schema returns the JSON schema for the delegation tool input.
//
// Returns:
//   - A tool.Schema describing the required subagent_type and message properties,
//   - plus optional run_in_background and handoff properties.
//
// Side effects:
//   - None.
func (d *DelegateTool) Schema() tool.Schema {
	categoryOptions := make([]string, 0, len(DefaultCategoryRouting()))
	for category := range DefaultCategoryRouting() {
		categoryOptions = append(categoryOptions, category)
	}

	schema := tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"category": {
				Type:        "string",
				Description: "The routing category to use for model selection",
				Enum:        categoryOptions,
			},
			"subagent_type": {
				Type:        "string",
				Description: "The specialised sub-agent type to delegate to",
			},
			"load_skills": {
				Type:        "array",
				Description: "Optional skills to load for the delegated task",
			},
			"session_id": {
				Type:        "string",
				Description: "Optional session identifier for continuation",
			},
			"message": {
				Type:        "string",
				Description: "The message or instruction to send to the target agent",
			},
			"run_in_background": {
				Type:        "boolean",
				Description: "If true, run the delegation asynchronously and return a task ID",
			},
			"handoff": {
				Type:        "object",
				Description: "Optional handoff metadata including ChainID for coordination",
			},
		},
		Required: []string{"subagent_type", "message"},
	}

	if d.registry != nil {
		manifests := d.registry.List()
		if len(manifests) > 0 {
			agentIDs := make([]string, 0, len(manifests))
			for _, m := range manifests {
				agentIDs = append(agentIDs, m.ID)
			}
			prop := schema.Properties["subagent_type"]
			prop.Enum = agentIDs
			schema.Properties["subagent_type"] = prop
		}
	}

	return schema
}

// Execute runs the delegation tool by routing the task to the appropriate sub-agent.
// When run_in_background is true and a background manager is configured, the task
// is executed asynchronously and returns a task ID immediately.
//
// Expected:
//   - ctx is a valid context for the delegation operation.
//   - input contains "subagent_type" and "message" string arguments.
//   - Optional "run_in_background" boolean to run asynchronously.
//   - Optional "handoff" object for ChainID and coordination.
//
// Returns:
//   - A tool.Result containing the sub-agent's aggregated response or task ID.
//   - An error if delegation is not allowed, arguments are invalid, or streaming fails.
//
// Side effects:
//   - Streams a request to the target agent's engine.
//   - Emits DelegationInfo stream chunks when an output channel is available in ctx.
func (d *DelegateTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	if !d.circuitBreaker.Allow() {
		return tool.Result{}, errCircuitBreakerOpen
	}

	if !d.delegation.CanDelegate {
		return tool.Result{}, errDelegationNotAllowed
	}

	params, err := d.parseDelegationParams(input)
	if err != nil {
		return tool.Result{}, err
	}

	if err := d.checkSpawnLimits(params.handoff); err != nil {
		return tool.Result{}, err
	}

	target, err := d.resolveTargetWithOptions(ctx, params)
	if err != nil {
		return tool.Result{}, err
	}

	outChan, hasOutput := streamOutputFromContext(ctx)
	chainID := target.chainID
	if chainID == "" {
		chainID = newDelegationChainID()
	}

	modelName := target.engine.LastModel()
	providerName := target.engine.LastProvider()
	if target.resolvedModel != "" {
		modelName = target.resolvedModel
	}
	if target.resolvedProvider != "" {
		providerName = target.resolvedProvider
	}

	baseInfo := provider.DelegationInfo{
		SourceAgent:  d.sourceAgentID,
		TargetAgent:  target.agentID,
		ChainID:      chainID,
		ModelName:    modelName,
		ProviderName: providerName,
		Description:  target.message,
		StartedAt:    ptrTime(time.Now().UTC()),
	}

	if params.runAsync {
		return d.executeAsync(ctx, target, baseInfo, outChan, hasOutput)
	}

	return d.executeSync(ctx, target, baseInfo, outChan, hasOutput)
}

// parseDelegationParams extracts delegation arguments into a typed parameter set.
//
// Expected:
//   - input contains delegation arguments accepted by the schema.
//
// Returns:
//   - Parsed delegation parameters.
//   - An error if the arguments are invalid.
//
// Side effects:
//   - None.
func (d *DelegateTool) parseDelegationParams(input tool.Input) (delegationParams, error) {
	params := delegationParams{}
	if err := populateDelegationRouting(&params, input.Arguments); err != nil {
		return delegationParams{}, err
	}
	if err := populateDelegationMetadata(&params, input.Arguments, d); err != nil {
		return delegationParams{}, err
	}

	return params, nil
}

// populateDelegationRouting copies routing fields from raw arguments into params.
//
// Expected:
//   - params is a non-nil destination.
//   - arguments contains delegation routing fields.
//
// Returns:
//   - An error if a routing value has the wrong type.
//
// Side effects:
//   - Writes parsed values into params.
func populateDelegationRouting(params *delegationParams, arguments map[string]interface{}) error {
	if raw, ok := arguments["category"]; ok && raw != nil {
		category, ok := raw.(string)
		if !ok {
			return errCategoryMustBeString
		}
		params.category = category
	}
	if raw, ok := arguments["subagent_type"]; ok && raw != nil {
		subagentType, ok := raw.(string)
		if !ok {
			return errSubagentTypeMustBeString
		}
		params.subagentType = subagentType
	}
	if params.category == "" && params.subagentType == "" {
		return errRoutingFieldRequired
	}
	return nil
}

// populateDelegationMetadata copies metadata fields from raw arguments into params.
//
// Expected:
//   - params is a non-nil destination.
//   - arguments contains delegation metadata fields.
//   - d is the delegate tool used to parse nested handoff data.
//
// Returns:
//   - An error if a metadata value has the wrong type or nested parsing fails.
//
// Side effects:
//   - Writes parsed values into params.
func populateDelegationMetadata(params *delegationParams, arguments map[string]interface{}, d *DelegateTool) error {
	message, ok := arguments["message"].(string)
	if !ok {
		return errMessageMustBeString
	}
	params.message = message

	if value, ok := arguments["run_in_background"].(bool); ok {
		params.runAsync = value
	}

	if raw, ok := arguments["handoff"]; ok && raw != nil {
		h, err := d.parseHandoff(raw)
		if err != nil {
			return fmt.Errorf("parsing handoff: %w", err)
		}
		params.handoff = h
	}

	if raw, ok := arguments["load_skills"]; ok && raw != nil {
		loadSkills, err := parseLoadSkills(raw)
		if err != nil {
			return err
		}
		params.loadSkills = loadSkills
	}

	if raw, ok := arguments["session_id"]; ok && raw != nil {
		sessionID, ok := raw.(string)
		if !ok {
			return errSessionIDMustBeString
		}
		params.sessionID = sessionID
	}

	return nil
}

// parseLoadSkills converts a raw load_skills argument into a slice of skill names.
//
// Expected:
//   - value is a JSON array decoded into []interface{}.
//
// Returns:
//   - A slice of skill names.
//   - An error if the value is not an array of strings.
//
// Side effects:
//   - None.
func parseLoadSkills(value interface{}) ([]string, error) {
	items, ok := value.([]interface{})
	if !ok {
		return nil, errLoadSkillsMustBeArray
	}

	loadSkills := make([]string, 0, len(items))
	for _, item := range items {
		skill, ok := item.(string)
		if !ok {
			return nil, errLoadSkillsMustBeArray
		}
		loadSkills = append(loadSkills, skill)
	}

	return loadSkills, nil
}

// executeSync runs delegation synchronously, blocking until complete.
//
// Expected:
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - A tool.Result with the delegation result.
//   - An error if delegation fails.
//
// Side effects:
//   - Emits delegation events.
func (d *DelegateTool) executeSync(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (tool.Result, error) {
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "started")

	delegateSessionID := fmt.Sprintf("delegate-%s-%d", target.agentID, time.Now().UTC().UnixNano())
	delegateCtx := context.WithValue(ctx, session.IDKey{}, delegateSessionID)

	chunks, err := target.engine.Stream(delegateCtx, target.agentID, target.message)
	if err != nil {
		d.circuitBreaker.RecordFailure()
		completedAt := time.Now().UTC()
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return tool.Result{}, fmt.Errorf("delegation failed: %w", err)
	}

	result, err := d.collectWithProgress(ctx, chunks, time.Now())
	if err != nil {
		d.circuitBreaker.RecordFailure()
		completedAt := time.Now().UTC()
		baseInfo.ToolCalls = result.toolCalls
		baseInfo.LastTool = result.lastTool
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return tool.Result{}, err
	}

	d.circuitBreaker.RecordSuccess()
	completedAt := time.Now().UTC()
	baseInfo.ModelName = target.engine.LastModel()
	baseInfo.ProviderName = target.engine.LastProvider()
	baseInfo.ToolCalls = result.toolCalls
	baseInfo.LastTool = result.lastTool
	baseInfo.CompletedAt = &completedAt
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "completed")

	return tool.Result{Output: result.response}, nil
}

// executeAsync runs delegation asynchronously, returning immediately with a task ID.
//
// Expected:
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - A tool.Result containing the task ID.
//   - An error if background mode is disabled or task launch fails.
//
// Side effects:
//   - Spawns a goroutine for the delegation.
//   - Emits delegation events for started status.
func (d *DelegateTool) executeAsync(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (tool.Result, error) {
	if d.backgroundManager == nil {
		return tool.Result{}, errBackgroundModeDisabled
	}

	taskID := fmt.Sprintf("task-%s-%d", target.agentID, time.Now().UTC().UnixNano())

	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "started")

	d.backgroundManager.Launch(ctx, taskID, target.agentID, target.message, func(ctx context.Context) (string, error) {
		delegateCtx := context.WithValue(ctx, session.IDKey{}, taskID)
		result, err := d.executeBackgroundTask(delegateCtx, target, baseInfo, outChan, hasOutput)
		if err != nil {
			return "", err
		}
		return result, nil
	})

	return tool.Result{Output: fmt.Sprintf(`{"task_id": %q, "status": "running"}`, taskID)}, nil
}

// executeBackgroundTask performs the actual delegation within a background goroutine.
//
// Expected:
//   - ctx is the task context with cancellation support.
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - The delegation result string on success.
//   - An error if delegation fails.
//
// Side effects:
//   - Emits delegation events for completed or failed status.
func (d *DelegateTool) executeBackgroundTask(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (string, error) {
	chunks, err := target.engine.Stream(ctx, target.agentID, target.message)
	if err != nil {
		d.circuitBreaker.RecordFailure()
		completedAt := time.Now().UTC()
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return "", fmt.Errorf("delegation failed: %w", err)
	}

	result, err := d.collectDelegationResult(chunks)
	if err != nil {
		d.circuitBreaker.RecordFailure()
		completedAt := time.Now().UTC()
		baseInfo.ToolCalls = result.toolCalls
		baseInfo.LastTool = result.lastTool
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return "", err
	}

	d.circuitBreaker.RecordSuccess()
	completedAt := time.Now().UTC()
	baseInfo.ModelName = target.engine.LastModel()
	baseInfo.ProviderName = target.engine.LastProvider()
	baseInfo.ToolCalls = result.toolCalls
	baseInfo.LastTool = result.lastTool
	baseInfo.CompletedAt = &completedAt
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "completed")

	return result.response, nil
}

// resolveTargetWithOptions validates input and resolves the target with async options.
//
// Expected:
//   - ctx is a valid context for the discovery operation.
//   - input contains subagent_type, message, run_in_background, and optional handoff arguments.
//
// Returns:
//   - The resolved target with chain ID.
//   - Whether to run asynchronously.
//   - An error if delegation is disabled, inputs are invalid, or no target exists.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveTargetWithOptions(ctx context.Context, params delegationParams) (delegationTarget, error) {
	if !d.delegation.CanDelegate {
		return delegationTarget{}, errDelegationNotAllowed
	}

	targetAgentID, err := d.resolveAgentID(ctx, params)
	if err != nil {
		return delegationTarget{}, err
	}

	if len(d.delegation.DelegationAllowlist) > 0 && !containsAgent(d.delegation.DelegationAllowlist, targetAgentID) {
		return delegationTarget{}, fmt.Errorf("%w: %q not in allowlist: %v",
			errAgentNotInAllowlist, targetAgentID, d.delegation.DelegationAllowlist)
	}

	var chainID string
	if params.handoff != nil {
		chainID = params.handoff.ChainID
	} else {
		chainID = newDelegationChainID()
	}

	targetEngine, ok := d.engines[targetAgentID]
	if !ok {
		return delegationTarget{}, fmt.Errorf("target agent engine not available: %s", targetAgentID)
	}

	var resolvedModel, resolvedProvider string
	if params.category != "" && d.categoryResolver != nil {
		if cfg, resolveErr := d.categoryResolver.Resolve(params.category); resolveErr == nil {
			resolvedModel = cfg.Model
			resolvedProvider = cfg.Provider
		}
	}

	if len(params.loadSkills) > 0 {
		manifest := targetEngine.Manifest()
		basePrompt := manifest.Instructions.SystemPrompt
		injectedPrompt := d.InjectSkillsIfProvided(params.loadSkills, basePrompt)
		manifest.Instructions.SystemPrompt = injectedPrompt
		targetEngine.SetManifest(manifest)
	}

	if params.sessionID == "" {
		targetEngine.SetSkipAgentFiles(true)
	} else {
		targetEngine.SetSkipAgentFiles(false)
	}

	return delegationTarget{
		agentID:          targetAgentID,
		engine:           targetEngine,
		message:          params.message,
		handoff:          params.handoff,
		chainID:          chainID,
		resolvedModel:    resolvedModel,
		resolvedProvider: resolvedProvider,
	}, nil
}

// resolveAgentID attempts registry lookup via subagent_type, then falls back to discovery.
//
// Expected:
//   - ctx is a valid context for discovery operations.
//   - params contains routing fields from the delegation input.
//
// Returns:
//   - The resolved agent ID on success.
//   - An error if no agent can be resolved.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveAgentID(ctx context.Context, params delegationParams) (string, error) {
	var registryErr error
	if params.subagentType != "" {
		resolvedID, err := d.ResolveByNameOrAlias(params.subagentType)
		if err == nil {
			return resolvedID, nil
		}
		registryErr = err
		if _, ok := d.engines[params.subagentType]; ok {
			return params.subagentType, nil
		}
	}

	if params.subagentType == "" {
		return "", errRoutingFieldRequired
	}

	id, discErr := d.resolveWithDiscovery(ctx, params.subagentType, params.message)
	if discErr != nil && registryErr != nil && d.registry != nil {
		return "", registryErr
	}
	return id, discErr
}

// resolveWithDiscovery attempts to resolve the target agent using embedding-based discovery.
//
// Expected:
//   - ctx is a valid context for the embedding operation.
//   - taskType is the delegation task type key.
//   - message is the delegation message for embedding.
//
// Returns:
//   - The resolved target agent ID.
//   - An error if resolution fails.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveWithDiscovery(ctx context.Context, taskType, message string) (string, error) {
	if d.embeddingDiscovery != nil {
		matches, err := d.embeddingDiscovery.Match(ctx, taskType+" "+message)
		if err == nil && len(matches) > 0 && matches[0].Confidence >= 0.7 {
			if _, ok := d.engines[matches[0].AgentID]; ok {
				return matches[0].AgentID, nil
			}
		}
	}

	return "", fmt.Errorf("no agent configured for task type: %s", taskType)
}

// parseHandoff parses a handoff argument into a delegation.Handoff struct.
//
// Expected:
//   - handoffArg is an interface{} that can be unmarshalled to Handoff.
//
// Returns:
//   - A parsed Handoff on success.
//   - An error if parsing fails.
//
// Side effects:
//   - None.
func (d *DelegateTool) parseHandoff(handoffArg interface{}) (*delegation.Handoff, error) {
	var h delegation.Handoff

	switch v := handoffArg.(type) {
	case map[string]interface{}:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, errHandoffMustBeObject
		}
		if err := json.Unmarshal(data, &h); err != nil {
			return nil, errHandoffMustBeObject
		}
	case string:
		if err := json.Unmarshal([]byte(v), &h); err != nil {
			return nil, errHandoffMustBeObject
		}
	default:
		return nil, errHandoffMustBeObject
	}

	return &h, nil
}

// collectDelegationResult aggregates streamed chunks from the delegated agent.
//
// Expected:
//   - chunks is the stream returned by the target engine.
//
// Returns:
//   - The concatenated response text.
//   - The number of chunks observed.
//   - The most recent tool name seen in the stream.
//   - An error if the stream yields a chunk error.
//
// Side effects:
//   - Reads from the streamed chunk channel until it closes or returns an error.
func (d *DelegateTool) collectDelegationResult(chunks <-chan provider.StreamChunk) (delegationResult, error) {
	var response strings.Builder
	toolCalls := 0
	lastTool := ""
	for chunk := range chunks {
		toolCalls++
		if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
			lastTool = chunk.ToolCall.Name
		}
		if chunk.Error != nil {
			return delegationResult{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
		}
		response.WriteString(chunk.Content)
	}

	return delegationResult{response: response.String(), toolCalls: toolCalls, lastTool: lastTool}, nil
}

// newDelegationChainID returns a unique identifier for a delegation chain.
//
// Returns:
//   - A chain identifier string derived from the current UTC time.
//
// Side effects:
//   - Reads the current clock to ensure uniqueness.
func newDelegationChainID() string {
	return fmt.Sprintf("chain-%d", time.Now().UTC().UnixNano())
}

// emitDelegationEvent sends a DelegationInfo chunk to the output channel when available.
//
// Expected:
//   - hasOutput indicates whether delegation events should be published.
//   - base contains the delegation metadata to reuse for the emitted chunk.
//
// Side effects:
//   - Attempts a non-blocking send to the output channel if it's still open.
//   - Silently drops events if the channel is full or closed (common when parent context is cancelled).
//   - Recovers from panic if the channel was closed by the parent context.
func (d *DelegateTool) emitDelegationEvent(
	outChan chan<- provider.StreamChunk, hasOutput bool,
	base provider.DelegationInfo, status string,
) {
	if !hasOutput {
		return
	}

	defer func() {
		if recover() == nil {
			return
		}
	}()

	info := base
	info.Status = status
	select {
	case outChan <- provider.StreamChunk{DelegationInfo: &info}:
	default:
	}
}

// deliverProgressEvent sends a ProgressEvent to the parent output channel when available.
//
// Expected:
//   - ctx carries the parent output channel via WithStreamOutput.
//   - toolCalls is the current count of tool invocations.
//   - lastTool is the name of the most recently invoked tool.
//   - startedAt is the time delegation began.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Attempts a non-blocking send to the parent output channel.
//   - Silently drops the event if the channel is full or absent.
func (d *DelegateTool) deliverProgressEvent(ctx context.Context, toolCalls int, lastTool string, startedAt time.Time) {
	outChan, ok := streamOutputFromContext(ctx)
	if !ok {
		return
	}

	activeDelegations := 0
	if d.backgroundManager != nil {
		activeDelegations = d.backgroundManager.ActiveCount()
	}

	ev := streaming.ProgressEvent{
		ToolCallCount:     toolCalls,
		LastTool:          lastTool,
		ActiveDelegations: activeDelegations,
		ElapsedTime:       time.Since(startedAt),
	}

	defer func() {
		if recover() == nil {
			return
		}
	}()
	select {
	case outChan <- provider.StreamChunk{Event: ev}:
	default:
	}
}

// collectWithProgress aggregates delegation chunks and periodically emits ProgressEvents.
//
// Expected:
//   - ctx carries the parent output channel for progress delivery.
//   - chunks is the stream channel from the child engine.
//   - startedAt is the delegation start time.
//
// Returns:
//   - A delegationResult with accumulated response, tool call count, and last tool name.
//   - An error if any chunk carries a stream error.
//
// Side effects:
//   - Emits ProgressEvents every 5 tool calls or every 5 seconds via deliverProgressEvent.
func (d *DelegateTool) collectWithProgress(
	ctx context.Context,
	chunks <-chan provider.StreamChunk,
	startedAt time.Time,
) (delegationResult, error) {
	var response strings.Builder
	toolCalls := 0
	lastTool := ""
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	const progressInterval = 5

	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return delegationResult{response: response.String(), toolCalls: toolCalls, lastTool: lastTool}, nil
			}
			toolCalls++
			if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
				lastTool = chunk.ToolCall.Name
			}
			if chunk.Error != nil {
				return delegationResult{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
			}
			response.WriteString(chunk.Content)
			if toolCalls%progressInterval == 0 {
				d.deliverProgressEvent(ctx, toolCalls, lastTool, startedAt)
			}
		case <-ticker.C:
			d.deliverProgressEvent(ctx, toolCalls, lastTool, startedAt)
		}
	}
}

// ptrTime returns a pointer to the supplied time.
//
// Expected:
//   - t is a valid time value to reference.
//
// Returns:
//   - A pointer to t.
//
// Side effects:
//   - None.
func ptrTime(t time.Time) *time.Time {
	return &t
}

// DelegateToAgent sends a message to a sub-agent and streams the response.
//
// Expected:
//   - ctx is a valid context for the delegation operation.
//   - engines is a map of agent IDs to their Engine instances.
//   - agentID identifies the delegation target directly.
//   - message is the instruction to send to the target agent.
//
// Returns:
//   - A channel of StreamChunk values from the target agent.
//   - An error if delegation is not allowed or the target agent is unavailable.
//
// Side effects:
//   - Initiates a streaming request on the target agent's engine.
func (e *Engine) DelegateToAgent(
	ctx context.Context,
	engines map[string]*Engine,
	agentID string,
	message string,
) (<-chan provider.StreamChunk, error) {
	if !e.manifest.Delegation.CanDelegate {
		return nil, errDelegationNotAllowed
	}

	targetEngine, ok := engines[agentID]
	if !ok {
		return nil, fmt.Errorf("target agent engine not available: %s", agentID)
	}

	return targetEngine.Stream(ctx, agentID, message)
}

// BackgroundManager returns the background task manager for this delegate tool.
//
// Returns:
//   - The BackgroundTaskManager if configured, or nil.
//
// Side effects:
//   - None.
func (d *DelegateTool) BackgroundManager() *BackgroundTaskManager {
	return d.backgroundManager
}

// CoordinationStore returns the coordination store for this delegate tool.
//
// Returns:
//   - The coordination.Store if configured, or nil.
//
// Side effects:
//   - None.
func (d *DelegateTool) CoordinationStore() coordination.Store {
	return d.coordinationStore
}

// HasEmbeddingDiscovery reports whether an embedding discovery has been wired.
//
// Returns:
//   - true when SetEmbeddingDiscovery has been called with a non-nil value.
//
// Side effects:
//   - None.
func (d *DelegateTool) HasEmbeddingDiscovery() bool {
	return d.embeddingDiscovery != nil
}

// SetDelegation updates the delegation configuration for this tool.
//
// Expected:
//   - config is the new delegation configuration to apply.
//
// Side effects:
//   - Replaces the internal delegation config used during Execute().
func (d *DelegateTool) SetDelegation(config agent.Delegation) {
	d.delegation = config
}

// SetSourceAgentID updates the source agent identifier for delegation event attribution.
//
// Expected:
//   - id is the identifier of the agent that owns this tool.
//
// Side effects:
//   - Replaces the internal sourceAgentID used during Execute().
func (d *DelegateTool) SetSourceAgentID(id string) {
	d.sourceAgentID = id
}

// Delegation returns the current delegation configuration.
//
// Returns:
//   - The agent.Delegation currently in use by this tool.
//
// Side effects:
//   - None.
func (d *DelegateTool) Delegation() agent.Delegation {
	return d.delegation
}

// CircuitBreaker returns the circuit breaker protecting the delegation flow.
//
// Returns:
//   - The CircuitBreaker instance used by this tool.
//
// Side effects:
//   - None.
func (d *DelegateTool) CircuitBreaker() *delegation.CircuitBreaker {
	return d.circuitBreaker
}

// InjectSkillsIfProvided prepends skill content to the base system prompt if loadSkills is non-empty.
//
// Expected:
//   - loadSkills is a slice of skill names to resolve.
//   - basePrompt is the initial system prompt to prepend skills to.
//
// Returns:
//   - The base prompt with skill content prepended (if resolver is available and loadSkills is non-empty).
//   - The base prompt unchanged if no resolver is configured or loadSkills is empty.
//
// Side effects:
//   - None.
func (d *DelegateTool) InjectSkillsIfProvided(loadSkills []string, basePrompt string) string {
	if d.skillResolver == nil || len(loadSkills) == 0 {
		return basePrompt
	}

	var skillContents []string
	for _, skillName := range loadSkills {
		content, err := d.skillResolver.Resolve(skillName)
		if err != nil {
			continue
		}
		marker := extractSkillMarker(content)
		if marker != "" && containsSkillMarker(basePrompt, marker) {
			continue
		}
		skillContents = append(skillContents, content)
	}

	if len(skillContents) == 0 {
		return basePrompt
	}

	return strings.Join(skillContents, "\n\n") + "\n\n" + basePrompt
}

// extractSkillMarker returns the first line of content if it starts with a
// Markdown heading (# or ##). This is used as a deduplication marker to avoid
// injecting the same skill twice into a prompt.
//
// Expected:
//   - content is a non-empty skill content string (may be empty, returns "").
//
// Returns:
//   - The first line when it begins with "# " or "## ".
//   - An empty string if content is empty or the first line is not a heading.
//
// Side effects:
//   - None.
func extractSkillMarker(content string) string {
	firstLine, _, _ := strings.Cut(content, "\n")
	if strings.HasPrefix(firstLine, "# ") || strings.HasPrefix(firstLine, "## ") {
		return firstLine
	}
	return ""
}

// containsSkillMarker reports whether marker appears as a complete line in prompt.
// This prevents false-positive prefix matches such as "# Skill: golang" matching
// against "# Skill: golang-testing".
//
// Expected:
//   - marker is a non-empty heading line extracted by extractSkillMarker.
//   - prompt is the base system prompt to search.
//
// Returns:
//   - true if marker appears as a standalone line (followed by "\n" or at end of string).
//
// Side effects:
//   - None.
func containsSkillMarker(prompt, marker string) bool {
	return strings.Contains(prompt, marker+"\n") || strings.HasSuffix(prompt, marker)
}

// Engines returns the delegate engine map keyed by agent ID.
//
// Returns:
//   - A map of agent ID to Engine for each delegation target.
//
// Side effects:
//   - None.
func (d *DelegateTool) Engines() map[string]*Engine {
	return d.engines
}

// containsAgent reports whether agentID appears in the allowlist slice.
//
// Expected:
//   - allowlist is a slice of agent ID strings (may be empty).
//   - agentID is the resolved agent identifier to search for.
//
// Returns:
//   - true if agentID matches any element in allowlist.
//
// Side effects:
//   - None.
func containsAgent(allowlist []string, agentID string) bool {
	for _, id := range allowlist {
		if id == agentID {
			return true
		}
	}
	return false
}
