// Package toolset provides default tool registrations.
package toolset

import (
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	"github.com/baphled/flowstate/internal/tool/read"
	"github.com/baphled/flowstate/internal/tool/web"
	"github.com/baphled/flowstate/internal/tool/write"
)

// NewDefaultRegistry creates a new tool registry with the default tools registered.
//
// Returns:
//   - A Registry containing bash, read, write, and web tools.
//
// Side effects:
//   - None.
func NewDefaultRegistry() *tool.Registry {
	r := tool.NewRegistry()
	r.Register(bash.New())
	r.Register(read.New())
	r.Register(write.New())
	r.Register(web.New())
	return r
}
