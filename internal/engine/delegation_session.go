package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// emitProgressHeartbeat periodically publishes delegation.progress events
// while the child stream runs. Restores parent-side visibility during long
// delegations without forwarding child content — the July 2026 tee gate
// removed the live content mirror that previously served this purpose.
//
// The goroutine exits when ctx is cancelled (caller-driven via defer
// cancel in executeSync).
//
// Expected:
//   - ctx is cancelled by the caller when the delegation completes,
//     fails, or the parent stream is torn down.
//   - baseInfo, parentSessionID, childSessionID, and loadSkills carry
//     the same values used for the delegation.started event.
//
// Side effects:
//   - Publishes a delegation.progress event every 30s onto the bus.
//
// Returns: result of emitProgressHeartbeat.
func (d *DelegateTool) emitProgressHeartbeat(
	ctx context.Context,
	baseInfo provider.DelegationInfo,
	parentSessionID, childSessionID string,
	loadSkills []string,
) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.publishDelegationEvent("progress",
				buildDelegationEventData(baseInfo, parentSessionID, childSessionID, "", loadSkills))
		}
	}
}

// buildDelegationEventData composes the bus payload from baseInfo + the
// resolved parent and child session ids and the optional model/provider
// strings the engine has on hand at the call site. Centralised so each of
// the six publish sites builds an identical-shape payload from its own
// locally-known fields.
//
// Expected:
//   - baseInfo carries the chunk-side delegation metadata (chainID,
//     source/target agents, description, started_at, etc).
//   - parentSessionID is the session that issued the delegate tool call.
//   - childSessionID is the session created or resumed for the delegate;
//     populated post-resolve.
//   - errMsg is non-empty only on failure paths.
//
// Returns:
//   - A populated DelegationEventData ready for publishDelegationEvent.
//
// Side effects:
//   - None.
func buildDelegationEventData(
	baseInfo provider.DelegationInfo,
	parentSessionID, childSessionID, errMsg string,
	loadSkills []string,
) events.DelegationEventData {
	data := events.DelegationEventData{
		ChainID:         baseInfo.ChainID,
		ParentSessionID: parentSessionID,
		ChildSessionID:  childSessionID,
		SourceAgent:     baseInfo.SourceAgent,
		TargetAgent:     baseInfo.TargetAgent,
		ModelName:       baseInfo.ModelName,
		ProviderName:    baseInfo.ProviderName,
		Description:     baseInfo.Description,
		ToolCalls:       baseInfo.ToolCalls,
		LastTool:        baseInfo.LastTool,
		CompletedAt:     baseInfo.CompletedAt,
		Error:           errMsg,
		LoadSkills:      loadSkills,
	}
	if baseInfo.StartedAt != nil {
		data.StartedAt = *baseInfo.StartedAt
	}
	return data
}

// persistSessionMetadata writes session metadata to disk on a best-effort basis.
// Errors are silently discarded so that persistence failures never block delegation.
//
// Expected:
//   - sess is the child session to persist.
//
// Returns:
//   - None.
//
// Side effects:
//   - Calls session.PersistSession when sessionsDir is non-empty.
func (d *DelegateTool) persistSessionMetadata(sess *session.Session) {
	if d.sessionsDir == "" {
		return
	}
	if err := session.PersistSession(d.sessionsDir, sess); err != nil {
		return
	}
}

// attachSessionStore creates a FileContextStore via the factory (if set) and
// attaches it to the target engine. Returns a cleanup function that flushes
// and closes the store. When no factory is configured, returns a no-op closer.
//
// Expected:
//   - eng is the delegation target engine.
//   - sessionID is the child session identifier.
//
// Returns:
//   - A cleanup function that must be called after the delegation stream is fully consumed.
//
// Side effects:
//   - When factory is set, calls SetContextStore on the engine, closing any previous store first.
func (d *DelegateTool) attachSessionStore(eng *Engine, sessionID string) func() {
	if d.storeFactory == nil {
		return func() {}
	}
	if existing := eng.ContextStore(); existing != nil {
		existing.Close()
	}
	store, err := d.storeFactory.CreateSessionStore(sessionID)
	if err != nil {
		return func() {}
	}
	eng.SetContextStore(store, sessionID)
	return func() {
		store.Close()
	}
}

// createChildSession registers a child session for the delegation and returns its ID.
// When a sessionCreator is configured and a parent session ID is present in ctx,
// it calls CreateWithParentAndChain to produce a traceable child session that
// carries the delegation chainID for cold-reload reconstruction (closes the hole
// left by a488b858 — Vue's runtime (chainId → childSessionId) map is empty after
// a hard reload because SwarmEvents do not replay on reconnect, so the persisted
// child session must self-describe its chain membership).
// On any error (including parent not found) it falls back to a synthetic ID.
//
// Expected:
//   - ctx may carry a parent session ID via session.IDKey{}.
//   - agentID identifies the delegated agent.
//   - chainID is the authoritative delegation chainID (may be empty when no
//     coordination chain is in flight; the chainID stamped on the child
//     session matches the value carried on the DelegationInfo event so the
//     frontend can correlate the two).
//
// Returns:
//   - The child session ID to use as the delegation context value.
//
// Side effects:
//   - May call sessionCreator.CreateWithParentAndChain, storing a new session in memory.
func (d *DelegateTool) createChildSession(ctx context.Context, agentID, chainID string) string {
	parentID := sessionIDFromContext(ctx)
	if d.sessionCreator != nil && parentID != "" {
		if child, err := d.sessionCreator.CreateWithParentAndChain(parentID, agentID, chainID); err == nil {
			d.persistSessionMetadata(child)
			return child.ID
		}
	}
	if d.sessionManager != nil && parentID != "" {
		if child, err := d.sessionManager.CreateWithParentAndChain(parentID, agentID, chainID); err == nil {
			d.persistSessionMetadata(child)
			return child.ID
		}
	}
	syntheticID := fmt.Sprintf("delegate-%s-%d", agentID, time.Now().UTC().UnixNano())
	if d.sessionManager != nil {
		d.sessionManager.RegisterSession(syntheticID, agentID)
	}
	return syntheticID
}

// resolveOrCreateSession returns an existing session when sessionID is found in the manager,
// or creates a new child session when not found or sessionID is empty.
//
// Expected:
//   - ctx may carry a parent session ID for new child session creation.
//   - agentID identifies the agent for the delegation.
//   - sessionID is the optional caller-supplied session to resume; empty means create new.
//   - chainID is the authoritative delegation chainID; stamped on a newly
//     created child session so cold-reload can rebuild the (chainId →
//     childSessionId) map without replaying SwarmEvents. Ignored when
//     resuming an existing session.
//
// Returns:
//   - The session ID to use for the delegation context.
//
// Side effects:
//   - May call sessionManager.GetSession or createChildSession.
func (d *DelegateTool) resolveOrCreateSession(ctx context.Context, agentID, sessionID, chainID string) string {
	if sessionID != "" && d.sessionManager != nil {
		if sess, err := d.sessionManager.GetSession(sessionID); err == nil {
			return sess.ID
		}
	}
	return d.createChildSession(ctx, agentID, chainID)
}

// memberTurnHandle carries the per-member child-Turn state between
// bootstrapMemberSession (which mints the Turn) and the buildMemberRunner
// closure (which Completes on success / Fails on dispatch error). The
// `ownedByCaller` flag mirrors executeSync's `turnOwnedByWrap` (PR2a
// §S4.2 R2 defence): the closure flips it to true the moment Complete
// fires on the happy path, so any subsequent failure surface that
// reaches failMemberTurnIfOwned short-circuits BEFORE Fail is invoked
// on a terminal Turn — preserving the single-source-of-correctness
// guard at the per-member layer that executeSync already provides at
// the single-target layer.
//
// Empty `turnID` means: registry was nil at bootstrap (legacy test
// composition) OR StartOrReuse failed soft. Every lifecycle site
// downstream nil-checks the turnID before invoking
// d.turnRegistry.* so back-compat for the dozens of pre-plumbing
// callsites holds per D7.
//
// Plans/Child Session Turn Registry Plumbing (May 2026) §Item 2d +
// §"Wire / data shapes" for the per-member layer. The shape is
// deliberately minimal — only the two fields the per-member closure
// reads; extending it to carry session id / chain prefix would invite
// downstream sites to read those off the handle instead of off the
// existing closure variables, which would couple the handle to seams
// it should stay agnostic of.
type memberTurnHandle struct {
	turnID        string
	ownedByCaller bool
}
