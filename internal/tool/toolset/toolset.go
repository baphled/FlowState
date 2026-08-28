// Package toolset provides default tool registrations.
package toolset

import (
	"time"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/questionrequest"
	"github.com/baphled/flowstate/internal/tool"
	applypatch "github.com/baphled/flowstate/internal/tool/apply_patch"
	"github.com/baphled/flowstate/internal/tool/bash"
	"github.com/baphled/flowstate/internal/tool/batch"
	"github.com/baphled/flowstate/internal/tool/edit"
	"github.com/baphled/flowstate/internal/tool/grep"
	"github.com/baphled/flowstate/internal/tool/invalid"
	"github.com/baphled/flowstate/internal/tool/ls"
	"github.com/baphled/flowstate/internal/tool/multiedit"
	"github.com/baphled/flowstate/internal/tool/plan"
	"github.com/baphled/flowstate/internal/tool/question"
	"github.com/baphled/flowstate/internal/tool/read"
	"github.com/baphled/flowstate/internal/tool/web"
	"github.com/baphled/flowstate/internal/tool/websearch"
	"github.com/baphled/flowstate/internal/tool/write"
)

// NewDefaultRegistry creates a new tool registry with the default tools registered.
//
// The question tool is omitted here — it requires the shared
// questionrequest.Registry that only the app layer can construct.
// Callers wire it via AppendQuestionTool on the app-built tool set.
//
// Returns:
//   - A Registry populated with the standard FlowState tools, including the
//     plan_list and plan_read tools bound to the process-wide plans
//     directory.
//
// Expected:
//   - websearchAPIKey contains the API key used by the websearch tool.
//   - plansDir is the directory where FlowState plan markdown files live
//     (typically ${DataDir}/plans). An empty string is permitted for tests
//     that do not exercise the plan tools.
//
// Side effects:
//   - Registers all default tools in a new registry.
func NewDefaultRegistry(websearchAPIKey, plansDir string) *tool.Registry {
	r := tool.NewRegistry()
	r.Register(bash.New())
	r.Register(batch.New(r))
	r.Register(read.New())
	r.Register(write.New())
	r.Register(edit.New())
	r.Register(multiedit.New())
	r.Register(plan.NewEnter())
	r.Register(plan.NewExit())
	r.Register(plan.NewList(plansDir))
	r.Register(plan.NewRead(plansDir))
	r.Register(plan.NewWrite(plansDir))
	r.Register(invalid.New())
	r.Register(applypatch.New())
	r.Register(web.New())
	r.Register(websearch.New("https://api.exa.ai/search", websearchAPIKey))
	// grep and ls are read-only filesystem enumeration: by design they are
	// the canonical path for agents working alongside the vault, so they
	// are not routed through the pathguard. Only mutating file ops (bash,
	// read, write, edit, multiedit, apply_patch) are guarded — see
	// app.buildToolsForManifestWithStore.
	r.Register(grep.New())
	r.Register(ls.New())
	return r
}

// AppendQuestionTool appends the blocking question tool bound to the
// shared questionrequest.Registry when reg is non-nil. The tool
// blocks on the operator's answer for the configured timeout (0 falls
// back to question.DefaultTimeout) and publishes its lifecycle events
// onto bus when bus is non-nil.
//
// Expected:
//   - base is the app-built tool slice to extend.
//   - reg is the shared questionrequest.Registry; nil skips the tool.
//   - bus is the event bus for lifecycle events; may be nil.
//   - timeout is the per-question suspension window; 0 means default.
//
// Returns:
//   - The extended tool slice.
//
// Side effects:
//   - None beyond constructing the tool.
func AppendQuestionTool(base []tool.Tool, reg *questionrequest.Registry, bus *eventbus.EventBus, timeout time.Duration) []tool.Tool {
	if reg == nil {
		return base
	}
	return append(base, question.NewWithBus(reg, bus, timeout))
}
