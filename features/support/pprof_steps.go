//go:build e2e

package support

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/cli"
)

// RegisterPprofSteps wires the flag-gated pprof instrumentation steps.
func RegisterPprofSteps(ctx *godog.ScenarioContext) {
	s := &pprofSteps{}
	ctx.Step(`^the serve command starts with no pprof flag$`, s.noPprofFlag)
	ctx.Step(`^no pprof server is listening$`, s.noPprofServerListening)
	ctx.Step(`^the serve command starts with pprof address "([^"]*)"$`, s.startsWithPprofAddress)
	ctx.Step(`^the pprof server is listening$`, s.pprofServerListening)
	ctx.Step(`^"GET /debug/pprof/" returns a profile index$`, s.pprofIndexServed)
	ctx.Step(`^serve startup fails with a non-loopback pprof address error$`, s.nonLoopbackRejected)
}

type pprofSteps struct {
	listener net.Listener
	lastErr  error
}

func (s *pprofSteps) noPprofFlag() error {
	if cli.DefaultPprofAddr() != "" {
		return fmt.Errorf("expected default pprof addr to be off, got %q", cli.DefaultPprofAddr())
	}
	return nil
}

func (s *pprofSteps) noPprofServerListening() error {
	if s.listener != nil {
		return errors.New("pprof listener unexpectedly active")
	}
	return nil
}

func (s *pprofSteps) startsWithPprofAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parsing addr %q: %w", addr, err)
	}
	ln, err := cli.StartPprofServerForTest(host + ":" + port)
	if err != nil {
		s.lastErr = err
		return nil
	}
	s.listener = ln
	return nil
}

func (s *pprofSteps) pprofServerListening() error {
	if s.lastErr != nil {
		return fmt.Errorf("pprof server failed to start: %w", s.lastErr)
	}
	if s.listener == nil {
		return errors.New("no pprof listener started")
	}
	return nil
}

func (s *pprofSteps) pprofIndexServed() error {
	if s.listener == nil {
		return errors.New("no pprof listener started")
	}
	url := "http://" + s.listener.Addr().String() + "/debug/pprof/"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading pprof index body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pprof index status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Types of profiles available") {
		return errors.New("pprof index body does not look like the profile index")
	}
	return nil
}

func (s *pprofSteps) nonLoopbackRejected() error {
	if s.lastErr == nil {
		return errors.New("expected non-loopback pprof address to be rejected")
	}
	if !strings.Contains(s.lastErr.Error(), "loopback") {
		return fmt.Errorf("unexpected rejection error: %v", s.lastErr)
	}
	return nil
}

func (s *pprofSteps) cleanup() {
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
}
