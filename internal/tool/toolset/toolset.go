// Package toolset provides default tool registrations.
package toolset

import (
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	"github.com/baphled/flowstate/internal/tool/file"
	"github.com/baphled/flowstate/internal/tool/web"
)

// NewDefaultRegistry creates a new tool registry with the default tools registered.
//
// Returns:
//   - A Registry containing bash, file, and web tools.
//
// Side effects:
//   - None.
func NewDefaultRegistry() *tool.Registry {
	r := tool.NewRegistry()
	r.Register(bash.New())
	r.Register(file.New())
	r.Register(web.New())
	return r
}
