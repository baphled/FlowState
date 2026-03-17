package toolset

import (
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	"github.com/baphled/flowstate/internal/tool/file"
	"github.com/baphled/flowstate/internal/tool/web"
)

func NewDefaultRegistry() *tool.Registry {
	r := tool.NewRegistry()
	r.Register(bash.New())
	r.Register(file.New())
	r.Register(web.New())
	return r
}
