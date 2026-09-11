//go:build e2e

package support

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/session"
)

// RegisterBatchedPersistenceSteps wires the batched session-persistence
// steps used by features/session/batched_persistence.feature.
func RegisterBatchedPersistenceSteps(ctx *godog.ScenarioContext) {
	s := &batchedPersistenceSteps{}
	ctx.Step(`^I append (\d+) messages to a persisted session$`, s.appendMessages)
	ctx.Step(`^the session sidecar has not yet been written$`, s.sidecarAbsent)
	ctx.Step(`^the in-memory session has (\d+) messages$`, s.inMemoryCount)
	ctx.Step(`^the session sidecar contains (\d+) messages$`, s.sidecarCount)
	ctx.Step(`^I flush pending session persists$`, s.flushPending)
	ctx.Step(`^I close the session$`, s.closeSession)
	ctx.Step(`^the session sidecar status is completed$`, s.sidecarCompleted)
}

type batchedPersistenceSteps struct {
	dir     string
	manager *session.Manager
	id      string
}

func (s *batchedPersistenceSteps) appendMessages(n int) error {
	if s.manager == nil {
		dir, err := os.MkdirTemp("", "bdd-batched-persist-*")
		if err != nil {
			return err
		}
		s.dir = dir
		s.manager = session.NewManager(nil)
		s.manager.SetSessionsDir(dir)
		sess, err := s.manager.CreateSession("bdd-agent")
		if err != nil {
			return err
		}
		s.id = sess.ID
	}
	for i := 0; i < n; i++ {
		s.manager.AppendMessage(s.id, session.Message{
			Role:    "user",
			Content: fmt.Sprintf("batched message %d", i),
		})
	}
	return nil
}

func (s *batchedPersistenceSteps) sidecarAbsent() error {
	if _, err := os.Stat(s.sidecarPath()); err == nil {
		return fmt.Errorf("sidecar written before flush threshold")
	}
	return nil
}

func (s *batchedPersistenceSteps) inMemoryCount(n int) error {
	sess, err := s.manager.GetSession(s.id)
	if err != nil {
		return err
	}
	if len(sess.Messages) != n {
		return fmt.Errorf("in-memory messages = %d, want %d", len(sess.Messages), n)
	}
	return nil
}

func (s *batchedPersistenceSteps) sidecarCount(n int) error {
	meta, err := s.readSidecar()
	if err != nil {
		return err
	}
	if len(meta.Messages) != n {
		return fmt.Errorf("sidecar messages = %d, want %d", len(meta.Messages), n)
	}
	return nil
}

func (s *batchedPersistenceSteps) flushPending() error {
	return s.manager.FlushPendingPersists()
}

func (s *batchedPersistenceSteps) closeSession() error {
	return s.manager.CloseSession(s.id)
}

func (s *batchedPersistenceSteps) sidecarCompleted() error {
	meta, err := s.readSidecar()
	if err != nil {
		return err
	}
	if meta.Status != "completed" {
		return fmt.Errorf("sidecar status = %q, want completed", meta.Status)
	}
	return nil
}

func (s *batchedPersistenceSteps) sidecarPath() string {
	return filepath.Join(s.dir, s.id+".meta.json")
}

func (s *batchedPersistenceSteps) readSidecar() (*session.Metadata, error) {
	data, err := os.ReadFile(s.sidecarPath())
	if err != nil {
		return nil, err
	}
	var meta session.Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}
