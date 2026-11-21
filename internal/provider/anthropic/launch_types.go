package anthropic

import (
	"bytes"
	"io"
	"os/exec"
	"sync"
)

// execProcess is the Process behind the production launcher.
type execProcess struct {
	cmd    *exec.Cmd
	out    io.ReadCloser
	errBuf *tailBuffer
	exited chan struct{} // closed once Wait has reaped the child
	once   sync.Once
}

// tailBuffer keeps only the last stderrTailLimit bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}
