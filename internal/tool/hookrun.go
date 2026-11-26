package tool

// Command-hook runner (ADR-012, packet 2a): runs one hook as an argv process, never a shell, with a scrubbed
// environment, capped stdin and output, a deadline, and its own process group on Unix. It reads the exit and JSON
// contract into a HookResult and decides nothing else: on_failure policy, points and recording belong to the
// callers. A hook gets no engine handle: the runner passes no extra descriptors and marks the lease files
// close-on-exec. The runner is safe for concurrent use.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Run starts the hook with stdin as its input document and reports what it did. It never returns an error: every
// way a hook can fail is a HookFailure result. The call returns only after the hook's process group has been
// stopped, when a stop was needed.
func (r HookRunner) Run(ctx context.Context, c HookCommand, stdin []byte) HookResult {
	started := time.Now()
	res := r.execute(ctx, c, stdin)
	res.Duration = time.Since(started)
	return res
}

// execute checks everything that must hold before a process starts, then runs it.
func (r HookRunner) execute(ctx context.Context, c HookCommand, stdin []byte) HookResult {
	if len(stdin) > HookStdinCap {
		return hookFailed(HookFailureInputTooLarge, "")
	}
	env, ok := hookEnv(c.EnvNames)
	if !ok {
		return hookFailed(HookFailureEnvRefused, "")
	}
	path, err := hookExecutable(c.Argv)
	if err != nil {
		return hookFailed(HookFailureStart, err.Error())
	}
	for _, f := range r.LeaseFiles {
		if err := markCloseOnExec(f); err != nil {
			return hookFailed(HookFailureLeaseFD, err.Error())
		}
	}
	return r.start(ctx, c, path, env, stdin)
}

// start runs the process under the hook's deadline and classifies the outcome.
func (r HookRunner) start(ctx context.Context, c HookCommand, path string, env []string, stdin []byte) HookResult {
	tctx, cancel := context.WithTimeout(ctx, hookTimeout(c.Timeout))
	defer cancel()
	out := &hookCapture{limit: HookStdoutCap, onOverflow: cancel}
	errOut := &hookCapture{limit: HookStderrCap}
	grace := r.Grace
	if grace <= 0 {
		grace = DefaultHookGrace
	}
	cmd := &exec.Cmd{Path: path, Args: append([]string{path}, c.Argv[1:]...), Dir: c.Dir, Env: env,
		Stdin: bytes.NewReader(stdin), Stdout: out, Stderr: errOut, WaitDelay: grace}
	stop, settle := isolateHook(cmd, grace)
	stopped, err := runHookCmd(tctx, cmd, stop)
	settle()
	res := HookResult{
		ExitCode: -1,
		Stdout:   SanitizeHookText(out.data.String(), true),
		Stderr:   SanitizeHookText(errOut.data.String(), true),
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	return classifyHook(res, err, hookEnded{parent: ctx, stopped: stopped, overflow: out.overflow}, c, out.data.Bytes(),
		errOut.data.String())
}

// classifyHook turns the process outcome into a verdict: exit 0 reads stdout, exit 2 denies with stderr as the
// reason, anything else is a failure. A run the runner stopped is a timeout or a cancellation whatever its exit
// status and stdout, so a hook that traps SIGTERM and exits 0 is never an allow; only an overflow stop, which
// the runner also issues, keeps its own code.
func classifyHook(res HookResult, err error, ended hookEnded, c HookCommand, stdout []byte, stderr string) HookResult {
	if ended.overflow {
		return failWith(res, HookFailureOutputTooLarge, "")
	}
	if ended.stopped {
		if ended.parent.Err() != nil {
			return failWith(res, HookFailureCanceled, "")
		}
		return failWith(res, HookFailureTimeout, "")
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return readHookStdout(res, c, stdout)
	case errors.Is(err, exec.ErrWaitDelay):
		return failWith(res, HookFailureOutputIncomplete, "")
	case errors.As(err, &exit) && res.ExitCode == HookDenyExit:
		res.Verdict = HookDeny
		res.Reason = boundHookText(SanitizeHookText(stderr, false), HookReasonCap)
		return res
	case errors.As(err, &exit):
		return failWith(res, HookFailureExit, "")
	}
	return failWith(res, HookFailureStart, err.Error())
}

// readHookStdout applies the stdout contract of an exit-0 hook: empty is allow, otherwise exactly one JSON object
// of the permitted shape.
func readHookStdout(res HookResult, c HookCommand, stdout []byte) HookResult {
	res.Verdict = HookAllow
	if len(bytes.TrimSpace(stdout)) == 0 {
		return res
	}
	var doc hookStdout
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return failWith(res, HookFailureInvalidOutput, "")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return failWith(res, HookFailureInvalidOutput, "")
	}
	if code := checkHookDoc(doc, c); code != "" {
		return failWith(res, code, "")
	}
	res.Output = HookOutput{Decision: doc.Decision, Args: doc.Args, Stop: doc.Stop != nil && *doc.Stop}
	res.Output.Reason = boundHookText(SanitizeHookText(doc.Reason, false), HookReasonCap)
	if doc.Decision == "deny" {
		res.Verdict = HookDeny
		res.Reason = res.Output.Reason
	}
	return res
}

// checkHookDoc validates a decoded stdout object and returns a failure code, or "" when it is acceptable.
func checkHookDoc(doc hookStdout, c HookCommand) string {
	switch {
	case doc.Context != nil:
		return HookFailureContextUnsupported
	case doc.Args != nil && !c.AllowArgs, doc.Stop != nil && !c.AllowStop:
		return HookFailureFieldNotPermitted
	case doc.Args != nil && !bytes.HasPrefix(bytes.TrimSpace(doc.Args), []byte("{")):
		return HookFailureInvalidOutput
	case doc.Decision != "" && doc.Decision != "allow" && doc.Decision != "deny":
		return HookFailureInvalidOutput
	case doc.Decision == "deny" && strings.TrimSpace(doc.Reason) == "":
		return HookFailureInvalidOutput
	}
	return ""
}

// hookFailed is a failure result for a hook that never started.
func hookFailed(code, detail string) HookResult {
	return failWith(HookResult{ExitCode: -1}, code, detail)
}

// failWith marks res as a failure with a code and a sanitised, bounded detail.
func failWith(res HookResult, code, detail string) HookResult {
	res.Verdict, res.Failure = HookFailure, code
	res.Detail = boundHookText(SanitizeHookText(detail, false), HookReasonCap)
	return res
}

// hookTimeout applies the default and the cap to a hook deadline.
func hookTimeout(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return DefaultHookTimeout
	case d > MaxHookTimeout:
		return MaxHookTimeout
	}
	return d
}

// hookExecutable resolves argv[0]: an absolute path as given, a bare name through PATH. A relative path with a
// separator is the loader's to resolve against the hook file, never the runner's.
func hookExecutable(argv []string) (string, error) {
	if len(argv) == 0 || argv[0] == "" {
		return "", errors.New("hook argv is empty")
	}
	name := argv[0]
	if strings.ContainsRune(name, os.PathSeparator) {
		if !filepath.IsAbs(name) {
			return "", errors.New("hook executable path is relative")
		}
		return name, nil
	}
	return exec.LookPath(name)
}

// hookEnv builds the scrubbed environment: PATH (plus SYSTEMROOT and USERPROFILE on Windows) and the listed
// names. A listed name that looks like a secret, is FLOWSTATE_*, or is malformed refuses the whole run, so passing
// a credential is never silent. Nothing else of the engine's environment is inherited.
func hookEnv(listed []string) ([]string, bool) {
	env := []string{"PATH=" + hookPathValue()}
	seen := map[string]bool{"PATH": true}
	names := listed
	if runtime.GOOS == "windows" {
		names = append([]string{"SYSTEMROOT", "USERPROFILE"}, listed...)
	}
	for _, name := range names {
		if name == "" || strings.ContainsAny(name, "=\x00") || execSecretEnv.MatchString(name) ||
			strings.HasPrefix(strings.ToUpper(name), "FLOWSTATE_") {
			return nil, false
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env, true
}

// hookPathValue is the engine's PATH, or the same fixed default exec.run uses when it is empty.
func hookPathValue() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
}

// Write keeps up to limit bytes and discards the rest while consuming all of it, so the hook never blocks on a
// full pipe. The first excess byte calls onOverflow, which stops a hook that floods stdout.
func (b *hookCapture) Write(p []byte) (int, error) {
	room := max(b.limit-b.data.Len(), 0)
	b.data.Write(p[:min(room, len(p))])
	if len(p) > room && !b.overflow {
		b.overflow = true
		if b.onOverflow != nil {
			b.onOverflow()
		}
	}
	return len(p), nil
}

// SanitizeHookText strips control, format and line-separator characters (the class swarm.Brief refuses) and
// invalid UTF-8, so a hook cannot paint a terminal or smuggle a line break. keepLayout keeps newline and tab,
// which captured output needs to stay readable. The session doctor strips the text it reads from a log with
// the same function (keepLayout false), so the writer and the reader cannot drift.
func SanitizeHookText(s string, keepLayout bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case keepLayout && (r == '\n' || r == '\t'):
			return r
		case r == utf8.RuneError, unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r),
			unicode.Is(unicode.Zp, r):
			return -1
		}
		return r
	}, s)
}

// boundHookText cuts s to at most limit bytes at a character boundary.
func boundHookText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// runHookCmd starts cmd and waits for it. When ctx ends first it stops the hook through stop (from
// isolateHook, SIGTERM to the group and SIGKILL after the grace) and still waits for the process, so the caller
// sees the process's own exit status. The bool (first) reports that a stop was issued, which the exit status cannot
// show: a hook may trap SIGTERM and exit 0. The Cmd is built from a trusted absolute path rather than exec.CommandContext
// so the call carries no tainted-argument finding, as exec.run does.
func runHookCmd(ctx context.Context, cmd *exec.Cmd, stop func() error) (bool, error) {
	if err := cmd.Start(); err != nil {
		return false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return false, err
	case <-ctx.Done():
		_ = stop()
		return true, <-done
	}
}
