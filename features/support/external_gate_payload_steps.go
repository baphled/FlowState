//go:build e2e

package support

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/gates"
	"github.com/baphled/flowstate/internal/swarm"
)

type externalGatePayloadState struct {
	dir      string
	response swarm.ExtGateResponse
	err      error
}

func RegisterExternalGatePayloadSteps(ctx *godog.ScenarioContext) {
	state := &externalGatePayloadState{}
	ctx.Step(`^an external gate executable that requires a task_plan field$`, state.externalGateRequiresTaskPlan)
	ctx.Step(`^FlowState invokes the external gate with this payload:$`, state.invokeExternalGate)
	ctx.Step(`^the external gate should pass$`, state.externalGateShouldPass)
}

func (s *externalGatePayloadState) externalGateRequiresTaskPlan() error {
	dir, err := os.MkdirTemp("", "flowstate-gate-payload-*")
	if err != nil {
		return err
	}
	s.dir = dir
	swarm.ResetExtGateRegistryForTest()
	script := `#!/usr/bin/env python3
import json, sys

req = json.load(sys.stdin)
payload = req.get("payload")
if isinstance(payload, str):
    try:
        payload = json.loads(payload)
    except Exception:
        pass

if isinstance(payload, dict) and payload.get("task_plan"):
    json.dump({"pass": True}, sys.stdout)
else:
    json.dump({"pass": False, "reason": f"payload_type={type(payload).__name__}"}, sys.stdout)
`
	path := filepath.Join(dir, "gate.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	return swarm.RegisterExtGateFromManifest(gates.Manifest{
		Name:    "payload-shape",
		Dir:     dir,
		Exec:    "./gate.py",
		Timeout: time.Second,
	})
}

func (s *externalGatePayloadState) invokeExternalGate(payload *godog.DocString) error {
	runner, ok := swarm.LookupExtGate("payload-shape")
	if !ok {
		return fmt.Errorf("payload-shape gate was not registered")
	}
	s.response, s.err = runner.Evaluate(context.Background(), swarm.ExtGateRequest{
		MemberID: "researcher",
		When:     swarm.LifecyclePostMember,
		Payload:  []byte(payload.Content),
	})
	return nil
}

func (s *externalGatePayloadState) externalGateShouldPass() error {
	defer os.RemoveAll(s.dir)
	if s.err != nil {
		return s.err
	}
	if !s.response.Pass {
		return fmt.Errorf("expected external gate to pass, got reason %q", s.response.Reason)
	}
	return nil
}
