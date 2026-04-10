package app

import (
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/learning"
)

// CreateLearningLoop builds and starts a LearningLoop from the given configuration.
//
// Expected:
//   - store is a non-nil learning.Store for persisting captured entries.
//   - cfg is a config.HarnessConfig describing which learning triggers are active.
//
// Returns:
//   - A started *learning.Loop that accepts triggers via Notify.
//
// Side effects:
//   - Spawns a background goroutine via LearningLoop.Run.
func CreateLearningLoop(store learning.Store, cfg config.HarnessConfig) *learning.Loop {
	var opts []learning.LoopOption
	if cfg.LearningOnFailure {
		opts = append(opts, learning.WithLearningOnFailure(true))
	}
	if cfg.LearningOnNovelty {
		opts = append(opts, learning.WithLearningOnNovelty(true))
	}
	loop := learning.NewLearningLoop(store, opts...)
	loop.Run()
	return loop
}

// LearningLoopHookFor returns a hook.Hook that notifies the given TriggerSink
// after each agent response, keyed by agentID.
//
// Expected:
//   - agentID identifies the agent whose responses are observed.
//   - sink is a non-nil learning.TriggerSink.
//
// Returns:
//   - A hook.Hook wired to the TriggerSink.
//
// Side effects:
//   - None; the hook itself spawns a goroutine per request inside hook.LearningLoopHook.
func LearningLoopHookFor(agentID string, sink learning.TriggerSink) hook.Hook {
	return hook.LearningLoopHook(agentID, sink)
}
