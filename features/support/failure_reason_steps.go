//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// failureReasonSteps holds the per-scenario state for the session
// failure_reason feature: a session manager writing into a temporary
// sessions dir, the wiring of the accumulator-driven cancel flow, and
// the captured output of the health CLI under a sandboxed cache dir.
type failureReasonSteps struct {
	mgr         *session.Manager
	sessionsDir string
	sessionID   string
	cancel      context.CancelFunc
	rawCh       chan provider.StreamChunk
	accumOut    <-chan provider.StreamChunk
	cliOutput   string
	cliCacheDir string
}

// RegisterFailureReasonSteps binds every step of
// features/session/failure_reason.feature: the sentinel status-flip
// scenarios (including the user-cancel sentinel), the failed-recovery
// demotion, and the provider health CLI surface.
func RegisterFailureReasonSteps(ctx *godog.ScenarioContext, st *failureReasonSteps) {
	ctx.Step(`^FlowState is running with failover enabled$`, st.flowStateIsRunningWithFailoverEnabled)
	ctx.Step(`^a session is in "([^"]*)" status$`, st.aSessionIsInStatus)
	ctx.Step(`^a session is in "([^"]*)" status with an in-flight turn$`, st.aSessionIsInStatusWithInflightTurn)
	ctx.Step(`^an assistant message arrives with StopReason "([^"]*)"$`, st.anAssistantMessageArrivesWithStopReason)
	ctx.Step(`^the user cancels the in-flight turn$`, st.theUserCancelsTheInflightTurn)
	ctx.Step(`^the session status is "([^"]*)"$`, st.theSessionStatusIs)
	ctx.Step(`^the meta\.json contains "([^"]*)" set to "([^"]*)"$`, st.theMetaJSONContainsSetTo)
	ctx.Step(`^a session has status "([^"]*)" and failure_reason "([^"]*)"$`, st.aSessionHasStatusAndFailureReason)
	ctx.Step(`^a healthy assistant message arrives$`, st.aHealthyAssistantMessageArrives)
	ctx.Step(`^the session status is demoted to "([^"]*)"$`, st.theSessionStatusIsDemotedTo)
	ctx.Step(`^failure_reason is cleared$`, st.failureReasonIsCleared)
	ctx.Step(`^no providers are rate-limited$`, st.noProvidersAreRateLimited)
	ctx.Step(`^"([^"]*)" \/ "([^"]*)" is rate-limited with (\d+)s cooldown$`, st.isRateLimitedWithCooldown)
	ctx.Step(`^the user runs "([^"]*)"$`, st.theUserRuns)
	ctx.Step(`^it prints "([^"]*)"$`, st.itPrints)
	ctx.Step(`^the output includes "([^"]*)"$`, st.theOutputIncludes)
	ctx.Step(`^"([^"]*)" \/ "([^"]*)" is no longer rate-limited$`, st.isNoLongerRateLimited)
}

func (st *failureReasonSteps) flowStateIsRunningWithFailoverEnabled() error {
	return nil
}

func (st *failureReasonSteps) ensureManager() error {
	if st.mgr != nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "flowstate-failure-reason-*")
	if err != nil {
		return fmt.Errorf("creating sessions dir: %w", err)
	}
	st.sessionsDir = dir
	st.mgr = session.NewManager(nil)
	st.mgr.SetSessionsDir(dir)
	return nil
}

func (st *failureReasonSteps) createActiveSession() error {
	if err := st.ensureManager(); err != nil {
		return err
	}
	sess, err := st.mgr.CreateSession("agent-failure-reason")
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	st.sessionID = sess.ID
	return nil
}

func (st *failureReasonSteps) aSessionIsInStatus(status string) error {
	if err := st.createActiveSession(); err != nil {
		return err
	}
	sess, err := st.mgr.GetSession(st.sessionID)
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if sess.Status != status {
		return fmt.Errorf("expected fresh session status %q, got %q", status, sess.Status)
	}
	return nil
}

func (st *failureReasonSteps) aSessionIsInStatusWithInflightTurn(status string) error {
	if err := st.aSessionIsInStatus(status); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	st.rawCh = make(chan provider.StreamChunk, 8)
	st.accumOut = session.AccumulateStream(ctx, st.mgr, st.sessionID, "agent-failure-reason", st.rawCh)
	st.rawCh <- provider.StreamChunk{Content: "partial answer"}
	select {
	case <-st.accumOut:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("in-flight turn did not forward its first content chunk")
	}
}

func (st *failureReasonSteps) theUserCancelsTheInflightTurn() error {
	st.cancel()
	st.rawCh <- provider.StreamChunk{Error: context.Canceled, Done: true}
	close(st.rawCh)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-st.accumOut:
			if !ok {
				return nil
			}
		case <-timeout:
			return errors.New("accumulator channel did not close after cancel")
		}
	}
}

func (st *failureReasonSteps) anAssistantMessageArrivesWithStopReason(reason string) error {
	wire, err := stopReasonWireValue(reason)
	if err != nil {
		return err
	}
	st.mgr.AppendMessage(st.sessionID, session.Message{
		Role:       "assistant",
		Content:    "assistant turn",
		StopReason: wire,
	})
	return nil
}

// stopReasonWireValue resolves a stop reason quoted in Gherkin to its
// wire value. The failure_reason feature quotes Go identifier names
// (StopReasonStreamTruncated) for the legacy sentinels and bare wire
// values (user_cancelled) for newer ones; both spellings must append
// the value the session manager actually compares against.
func stopReasonWireValue(reason string) (string, error) {
	switch reason {
	case session.StopReasonStreamTruncated,
		session.StopReasonToolUseNoCalls,
		session.StopReasonAbandonedTool,
		session.StopReasonToolLoopExceeded,
		session.StopReasonUserCancelled,
		session.StopReasonContextWindowExceeded:
		return reason, nil
	case "StopReasonStreamTruncated":
		return session.StopReasonStreamTruncated, nil
	case "StopReasonToolUseNoCalls":
		return session.StopReasonToolUseNoCalls, nil
	case "StopReasonAbandonedTool":
		return session.StopReasonAbandonedTool, nil
	case "StopReasonToolLoopExceeded":
		return session.StopReasonToolLoopExceeded, nil
	case "StopReasonContextWindowExceeded":
		return session.StopReasonContextWindowExceeded, nil
	}
	return "", fmt.Errorf("unknown stop reason %q", reason)
}

func (st *failureReasonSteps) theSessionStatusIs(status string) error {
	sess, err := st.mgr.GetSession(st.sessionID)
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if sess.Status != status {
		return fmt.Errorf("expected session status %q, got %q", status, sess.Status)
	}
	return nil
}

func (st *failureReasonSteps) theMetaJSONContainsSetTo(key, value string) error {
	wire, err := stopReasonWireValue(value)
	if err != nil {
		return err
	}
	loaded, err := session.LoadSessionMetadata(st.sessionsDir, st.sessionID)
	if err != nil {
		return fmt.Errorf("loading meta.json: %w", err)
	}
	if loaded == nil {
		return errors.New("meta.json sidecar not found for session")
	}
	if key != "failure_reason" {
		return fmt.Errorf("unsupported meta.json key %q", key)
	}
	if loaded.FailureReason != wire {
		return fmt.Errorf("expected meta.json %q to be %q, got %q", key, wire, loaded.FailureReason)
	}
	return nil
}

func (st *failureReasonSteps) aSessionHasStatusAndFailureReason(status, reason string) error {
	wire, err := stopReasonWireValue(reason)
	if err != nil {
		return err
	}
	if err := st.createActiveSession(); err != nil {
		return err
	}
	for i := 0; i < session.DefaultToolAnomalyStreakCap; i++ {
		st.mgr.AppendMessage(st.sessionID, session.Message{
			Role:       "assistant",
			Content:    "terminal turn",
			StopReason: wire,
		})
	}
	sess, err := st.mgr.GetSession(st.sessionID)
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if sess.Status != status {
		return fmt.Errorf("expected session status %q, got %q", status, sess.Status)
	}
	if sess.FailureReason != wire {
		return fmt.Errorf("expected failure_reason %q, got %q", wire, sess.FailureReason)
	}
	return nil
}

func (st *failureReasonSteps) aHealthyAssistantMessageArrives() error {
	st.mgr.AppendMessage(st.sessionID, session.Message{
		Role:       "assistant",
		Content:    "healthy follow-up answer",
		StopReason: "end_turn",
	})
	return nil
}

func (st *failureReasonSteps) theSessionStatusIsDemotedTo(status string) error {
	return st.theSessionStatusIs(status)
}

func (st *failureReasonSteps) failureReasonIsCleared() error {
	sess, err := st.mgr.GetSession(st.sessionID)
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if sess.FailureReason != "" {
		return fmt.Errorf("expected failure_reason to be cleared, got %q", sess.FailureReason)
	}
	return nil
}

func (st *failureReasonSteps) healthSandbox() error {
	if st.cliCacheDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "flowstate-health-cache-*")
	if err != nil {
		return fmt.Errorf("creating health cache sandbox: %w", err)
	}
	st.cliCacheDir = dir
	if err := os.Setenv("XDG_CACHE_HOME", dir); err != nil {
		return fmt.Errorf("setting XDG_CACHE_HOME: %w", err)
	}
	return nil
}

func (st *failureReasonSteps) noProvidersAreRateLimited() error {
	if err := st.healthSandbox(); err != nil {
		return err
	}
	hm := failover.NewHealthManager()
	if err := hm.ResetProviderHealth("", ""); err != nil {
		return fmt.Errorf("seeding empty health state: %w", err)
	}
	return nil
}

func (st *failureReasonSteps) isRateLimitedWithCooldown(providerName, model string, cooldownSec int) error {
	if err := st.healthSandbox(); err != nil {
		return err
	}
	hm := failover.NewHealthManager()
	hm.MarkRateLimited(providerName, model, time.Now().Add(time.Duration(cooldownSec)*time.Second))
	if err := hm.Flush(); err != nil {
		return fmt.Errorf("persisting rate-limit state: %w", err)
	}
	return nil
}

func (st *failureReasonSteps) theUserRuns(command string) error {
	parts := strings.Fields(command)
	if len(parts) < 2 || parts[0] != "flowstate" {
		return fmt.Errorf("unsupported command %q", command)
	}
	args := parts[1:]
	if len(args) == 4 && args[0] == "health" && args[1] == "reset" {
		args = []string{"health", "reset", args[2] + "/" + args[3]}
	}
	if err := st.healthSandbox(); err != nil {
		return err
	}
	if err := st.buildCLI(); err != nil {
		return err
	}
	home, err := os.MkdirTemp("", "flowstate-health-home-*")
	if err != nil {
		return fmt.Errorf("creating CLI home sandbox: %w", err)
	}
	cfgDir := filepath.Join(home, ".config", "flowstate")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return fmt.Errorf("creating CLI config dir: %w", err)
	}
	cfgYAML := "providers:\n  default: ollama\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfgYAML), 0o600); err != nil {
		return fmt.Errorf("writing CLI config: %w", err)
	}
	cmd := exec.Command("/tmp/flowstate-test", args...)
	cmd.Env = []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + st.cliCacheDir,
		"PATH=" + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	st.cliOutput = string(out)
	if err != nil {
		return fmt.Errorf("running %q: %w: %s", command, err, st.cliOutput)
	}
	return nil
}

func (st *failureReasonSteps) buildCLI() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolving cwd: %w", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(cwd))
	build := exec.Command("go", "build", "-o", "/tmp/flowstate-test", "./cmd/flowstate")
	build.Dir = projectRoot
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building CLI: %w: %s", err, string(out))
	}
	return nil
}

func (st *failureReasonSteps) itPrints(text string) error {
	if !strings.Contains(st.cliOutput, text) {
		return fmt.Errorf("expected CLI output to print %q, got: %s", text, st.cliOutput)
	}
	return nil
}

func (st *failureReasonSteps) theOutputIncludes(text string) error {
	if !strings.Contains(st.cliOutput, text) {
		return fmt.Errorf("expected CLI output to include %q, got: %s", text, st.cliOutput)
	}
	return nil
}

func (st *failureReasonSteps) isNoLongerRateLimited(providerName, model string) error {
	hm := failover.NewHealthManager()
	if err := hm.LoadState(hm.PersistPath()); err != nil {
		return fmt.Errorf("loading health state: %w", err)
	}
	if hm.IsRateLimited(providerName, model) {
		return fmt.Errorf("expected %q / %q to no longer be rate-limited", providerName, model)
	}
	return nil
}
