// Package toolset provides default tool registrations.
package toolset

import (
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
	r.Register(question.New())
	r.Register(plan.NewEnter())
	r.Register(plan.NewExit())
	r.Register(plan.NewList(plansDir))
	r.Register(plan.NewRead(plansDir))
	r.Register(invalid.New())
	r.Register(applypatch.New())
	r.Register(web.New())
	r.Register(websearch.New("https://api.exa.ai/search", websearchAPIKey))
	r.Register(grep.New())
	r.Register(ls.New())
	return r
}
