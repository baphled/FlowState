package anthropic

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// command validates req and builds the invocation; no process is started. The
// returned cleanup removes the working directory, and only when the default
// factory created it in this call; a supplied factory's directory is the
// caller's.
func (p *Subscription) command(req *provider.ChatRequest) (cmd Command, cleanup func(), err error) {
	if p.launch == nil {
		return Command{}, nil, ErrNoLauncher
	}
	if req == nil {
		return Command{}, nil, fmt.Errorf("%w: nil request", ErrInvalidRequest)
	}
	if err := rejectTools(req); err != nil {
		return Command{}, nil, err
	}
	prompt, system, err := mapMessages(req.Messages)
	if err != nil {
		return Command{}, nil, err
	}
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	if !validModel(model) {
		return Command{}, nil, fmt.Errorf("%w: %q", ErrInvalidModel, model)
	}
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--no-session-persistence", "--tools", "", "--strict-mcp-config", "--setting-sources", "",
		"--model", model,
	}
	if system != "" {
		// The = form binds the text to the flag, so a system prompt that
		// starts with a dash cannot be read as another option.
		args = append(args, "--system-prompt="+system)
	}
	// A fresh private directory, never the shared temp dir itself, so a
	// planted CLAUDE.md there cannot reach the model. Created last, so a
	// refused request leaves nothing behind.
	dir, cleanup, err := p.newWorkDir()
	if err != nil {
		return Command{}, nil, err
	}
	return Command{Bin: p.bin, Args: args, Dir: dir, Stdin: prompt}, cleanup, nil
}

// newWorkDir returns a usable working directory and the function that removes
// it: real for the default factory, a no-op for a supplied one.
func (p *Subscription) newWorkDir() (dir string, cleanup func(), err error) {
	factory, owned := p.workDir, false
	if factory == nil {
		factory, owned = privateWorkDir, true
	}
	if dir, err = factory(); err != nil {
		return "", nil, fmt.Errorf("anthropic: private working directory: %w", err)
	}
	if owned {
		return dir, func() { removeDefaultDir(dir) }, nil
	}
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return "", nil, fmt.Errorf("%w: %q", ErrInvalidWorkDir, dir)
	}
	return dir, func() {}, nil
}

// validModel accepts Claude Code aliases and model ids: it must start with
// a letter or digit, so it can never be read as a flag, and may then use
// letters, digits and . _ : [ ] -.
func validModel(m string) bool {
	for i, r := range m {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if alnum || (i > 0 && strings.ContainsRune("._:[]-", r)) {
			continue
		}
		return false
	}
	return m != ""
}

// rejectTools fails closed on any structured tool surface.
func rejectTools(req *provider.ChatRequest) error {
	if len(req.Tools) > 0 {
		return ErrUnsupportedTools
	}
	for i := range req.Messages {
		m := &req.Messages[i]
		if len(m.ToolCalls) > 0 || m.ToolCallID != "" || m.Role == "tool" {
			return ErrUnsupportedTools
		}
	}
	return nil
}

// mapMessages returns the stdin prompt and the joined system prompt. The
// prompt is always a "User:" / "Assistant:" transcript ending with the latest
// message, even for one message: a prompt starting with "/" would be run by
// Claude Code as a local slash command instead of reaching the model.
func mapMessages(msgs []provider.Message) (prompt, system string, err error) {
	var sys, rest []string
	hasUser := false
	for _, m := range msgs {
		label := "User"
		switch m.Role {
		case "system":
			sys = append(sys, m.Content)
			continue
		case "user":
			hasUser = hasUser || strings.TrimSpace(m.Content) != ""
		case "assistant":
			label = "Assistant"
		default:
			return "", "", fmt.Errorf("%w: unsupported message role %q", ErrInvalidRequest, m.Role)
		}
		rest = append(rest, label+":\n"+m.Content)
	}
	if !hasUser {
		return "", "", fmt.Errorf("%w: no non-blank user message", ErrInvalidRequest)
	}
	system = strings.Join(sys, "\n\n")
	if len(system) > MaxSystemPromptBytes {
		return "", "", fmt.Errorf("%w: %d bytes exceeds the %d-byte limit",
			ErrSystemPromptTooLarge, len(system), MaxSystemPromptBytes)
	}
	return strings.Join(rest, "\n\n"), system, nil
}
