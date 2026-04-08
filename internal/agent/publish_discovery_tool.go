package agent

import (
	"errors"

	"github.com/baphled/flowstate/internal/recall"
)

// PublishDiscoveryTool is a tool for publishing discovery events.
type PublishDiscoveryTool struct {
	Kind     string
	Summary  string
	Details  string
	Affects  string
	Priority string
	Evidence string
	Store    interface {
		Publish(*recall.Discovery) (string, error)
	}
}

// Run validates fields and publishes the discovery event.
func (t *PublishDiscoveryTool) Run() (string, error) {
	if t.Kind == "" || t.Summary == "" || t.Details == "" {
		return "", errors.New("kind, summary, and details are required")
	}
	d := &recall.Discovery{
		Kind:     t.Kind,
		Summary:  t.Summary,
		Details:  t.Details,
		Affects:  t.Affects,
		Priority: t.Priority,
		Evidence: t.Evidence,
	}
	id, err := t.Store.Publish(d)
	if err != nil {
		return "", err
	}
	return id, nil
}
